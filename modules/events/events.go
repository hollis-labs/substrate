// Package events is the event-stream module: a replayable fan-out hub (libs
// ui-go streamhub) served as Server-Sent Events at GET /api/events, the stream
// the app-runtime useSSE hook consumes. Substrate owns the plumbing, never the
// meaning: the app publishes events and decides who may read a topic.
//
// Wire convention, for every topic:
//
//	GET /api/events?topic=<topic>[&after=<seq>]
//	Last-Event-ID: <seq>      (sent by the browser on reconnect; wins over ?after)
//
//	id: <seq>  event: <name>  data: <bytes>      one frame per published event
//	event: gap  data: {"reason","requested","oldest_available","latest","missed"}
//
// With no cursor the stream is live only; with one it replays what the log
// retains after it. A gap frame says records were lost (retention or a slow
// consumer) rather than papering over it. Heartbeat comments keep proxies open.
// Errors before the stream starts use the flat apierr body.
//
// Config under "events": retention_records (default 1024 per topic),
// scope (replaces the default "<app>.events" scope).
package events

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/ssekit"
	"github.com/hollis-labs/libs/ui/ui-go/streamhub"

	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/ui"
)

// HubKey is where the *streamhub.Hub is published.
const HubKey = "events.hub"

//go:embed ui.json
var uiJSON []byte

var (
	mu         sync.Mutex
	customLog  streamhub.Log
	authorizer func(*http.Request, string) error
	topicRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,127}$`)
)

// UseLog supplies a durable streamhub.Log (the app's own tables) in place of the
// default in-memory ring. Call before boot.Start.
func UseLog(l streamhub.Log) { mu.Lock(); customLog = l; mu.Unlock() }

// UseTopicAuthorizer sets who may read which topic, beyond the route's scope.
// Return an error to refuse (reported as 403). Call before boot.Start. Without
// one every caller who passes the route scope may read every topic.
func UseTopicAuthorizer(f func(r *http.Request, topic string) error) {
	mu.Lock()
	authorizer = f
	mu.Unlock()
}

func init() { module.Register(&mod{}) }

type mod struct{ hub *streamhub.Hub }

func (*mod) Name() string { return "events" }

func (*mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }

func (m *mod) Start(_ context.Context, app *module.App) error {
	records := 1024
	if cfg, ok := app.Config["events"].(map[string]any); ok {
		switch v := cfg["retention_records"].(type) {
		case float64:
			records = int(v)
		}
	}
	mu.Lock()
	log, auth := customLog, authorizer
	mu.Unlock()
	if log == nil {
		log = streamhub.NewMemoryLog(streamhub.WithMemoryRetention(streamhub.Retention{MaxRecords: records}))
	}
	m.hub = streamhub.New(log)
	app.Set(HubKey, m.hub)
	return module.AddRoutes(app, m.UI(), map[string]http.Handler{"GET /api/events": serve(m.hub, auth)})
}

func (m *mod) Stop(ctx context.Context) error {
	if m.hub == nil {
		return nil
	}
	err := m.hub.Shutdown(ctx)
	m.hub = nil
	return err
}

// Hub returns the hub published by the events module, or nil.
func Hub(app *module.App) *streamhub.Hub {
	v, _ := app.Value(HubKey)
	h, _ := v.(*streamhub.Hub)
	return h
}

// Publish appends an event to topic and delivers it to live subscribers.
func Publish(ctx context.Context, app *module.App, topic, name string, data []byte) (streamhub.Seq, error) {
	h := Hub(app)
	if h == nil {
		return 0, errors.New("events module is not started")
	}
	if !topicRe.MatchString(topic) {
		return 0, fmt.Errorf("events: invalid topic %q", topic)
	}
	rec, err := h.Publish(ctx, topic, streamhub.Event{Name: name, Data: data})
	return rec.Seq, err
}

func serve(hub *streamhub.Hub, auth func(*http.Request, string) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		topic := r.URL.Query().Get("topic")
		if !topicRe.MatchString(topic) {
			apierr.Write(w, http.StatusBadRequest, "invalid_topic", "topic is required: lower-case letters, digits and _.:- only", nil)
			return
		}
		if auth != nil {
			if err := auth(r, topic); err != nil {
				apierr.Write(w, http.StatusForbidden, "forbidden", "not allowed to read this topic", nil)
				return
			}
		}
		after := streamhub.FromLatest
		if cur, ok := ssekit.ResumeCursor(r, ssekit.WithQueryKeys("after")); ok {
			n, err := strconv.ParseUint(cur, 10, 64)
			if err != nil {
				apierr.Write(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a sequence number", nil)
				return
			}
			after = streamhub.Seq(n)
		}
		sse, err := ssekit.NewWriter(w)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, "no_streaming", "response does not support streaming", nil)
			return
		}
		sub, err := hub.Subscribe(r.Context(), topic, streamhub.SubscribeOptions{After: after})
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, "unavailable", "event stream is not available", nil)
			return
		}
		defer func() { _ = sub.Close() }()
		src := ssekit.SourceFunc(func(ctx context.Context) (ssekit.Event, error) {
			it, err := sub.Next(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return ssekit.Event{}, io.EOF
				}
				return ssekit.Event{}, err
			}
			if g := it.Gap; g != nil {
				b, _ := json.Marshal(map[string]any{
					"reason": g.Reason.String(), "requested": g.Requested,
					"oldest_available": g.OldestAvailable, "latest": g.Latest, "missed": g.Missed,
				})
				return ssekit.Event{Name: "gap", Data: b}, nil
			}
			return ssekit.Event{ID: strconv.FormatUint(uint64(it.Record.Seq), 10), Name: it.Record.Name, Data: it.Record.Data}, nil
		})
		// Serve ends on client disconnect, hub shutdown or a source error; the
		// client reconnects with Last-Event-ID, so none of those need a reply here.
		_ = ssekit.Serve(r.Context(), sse, src, ssekit.WithHeartbeat(15*time.Second))
	})
}
