package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/ui/ui-go/ssekit"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/events"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
)

func serve(t *testing.T, mods []string, cfg string) (*boot.Running, *httptest.Server) {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", mods, cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); _ = run.Stop(context.Background()) })
	return run, srv
}

func open(t *testing.T, url string, hdr map[string]string) (*http.Response, func() (ssekit.Event, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	next, stop := iterNext(resp)
	t.Cleanup(stop)
	return resp, next
}

func iterNext(resp *http.Response) (func() (ssekit.Event, error), func()) {
	type item struct {
		ev  ssekit.Event
		err error
	}
	ch := make(chan item)
	done := make(chan struct{})
	go func() {
		for ev, err := range ssekit.Read(resp.Body) {
			select {
			case ch <- item{ev, err}:
			case <-done:
				return
			}
		}
		close(ch)
	}()
	return func() (ssekit.Event, error) {
			select {
			case it, ok := <-ch:
				if !ok {
					return ssekit.Event{}, context.Canceled
				}
				return it.ev, it.err
			case <-time.After(3 * time.Second):
				return ssekit.Event{}, context.DeadlineExceeded
			}
		}, func() {
			close(done)
		}
}

var mods = []string{"health", "httphost", "events"}

func TestLiveEventsThenResumeReplaysWhatWasMissed(t *testing.T) {
	run, srv := serve(t, mods, `{}`)
	ctx := context.Background()

	resp, next := open(t, srv.URL+"/api/events?topic=jobs", nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}
	// The subscription is live only, so publish once the stream is open. The
	// first frame (or heartbeat) proves headers were flushed.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = events.Publish(ctx, run.App, "jobs", "progress", []byte(`{"n":1}`))
	}()
	ev, err := next()
	if err != nil || ev.Name != "progress" || ev.ID != "1" || string(ev.Data) != `{"n":1}` {
		t.Fatalf("got %+v, %v", ev, err)
	}

	// Two more while "disconnected"; a reconnect with Last-Event-ID replays them.
	_, _ = events.Publish(ctx, run.App, "jobs", "progress", []byte(`{"n":2}`))
	_, _ = events.Publish(ctx, run.App, "jobs", "done", []byte(`{"n":3}`))
	_, next2 := open(t, srv.URL+"/api/events?topic=jobs", map[string]string{"Last-Event-ID": "1"})
	for _, want := range []struct{ id, name string }{{"2", "progress"}, {"3", "done"}} {
		ev, err := next2()
		if err != nil || ev.ID != want.id || ev.Name != want.name {
			t.Fatalf("replay: got %+v, %v; want %+v", ev, err, want)
		}
	}
}

func TestCursorAheadIsReportedAsAGapNotHidden(t *testing.T) {
	run, srv := serve(t, mods, `{}`)
	_, _ = events.Publish(context.Background(), run.App, "t1", "a", []byte("x"))
	_, next := open(t, srv.URL+"/api/events?topic=t1&after=99", nil)
	ev, err := next()
	if err != nil || ev.Name != "gap" {
		t.Fatalf("got %+v, %v", ev, err)
	}
	var g map[string]any
	if json.Unmarshal(ev.Data, &g) != nil || g["reason"] == "" || g["latest"] == nil {
		t.Fatalf("gap payload %s", ev.Data)
	}
}

func TestBadRequestsUseTheFlatErrorBody(t *testing.T) {
	_, srv := serve(t, mods, `{}`)
	for _, c := range []struct{ name, query, code string }{
		{"missing topic", "", "invalid_topic"},
		{"bad topic", "?topic=Has%20Space", "invalid_topic"},
		{"bad cursor", "?topic=ok&after=zzz", "invalid_cursor"},
	} {
		resp, err := http.Get(srv.URL + "/api/events" + c.query)
		if err != nil {
			t.Fatal(err)
		}
		var b struct{ Code, Message string }
		_ = json.NewDecoder(resp.Body).Decode(&b)
		_ = resp.Body.Close()
		if resp.StatusCode != 400 || b.Code != c.code || b.Message == "" {
			t.Errorf("%s: %d %+v", c.name, resp.StatusCode, b)
		}
	}
}

func TestRouteScopeIsEnforcedAndTopicAuthorizerRefuses(t *testing.T) {
	events.UseTopicAuthorizer(func(_ *http.Request, topic string) error {
		if topic == "secret" {
			return context.Canceled
		}
		return nil
	})
	t.Cleanup(func() { events.UseTopicAuthorizer(nil) })
	reader, other := "reader-key", "other-key"
	cfg := `{"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/r","hash":"` + libauth.HashKey(reader) + `","scopes":["t.events"]},` +
		`{"id":"svc/o","hash":"` + libauth.HashKey(other) + `","scopes":["t.read"]}]}}`
	_, srv := serve(t, []string{"health", "auth", "httphost", "events"}, cfg)
	status := func(path, key string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err != nil {
			return 200 // headers flushed and the stream is open: it was admitted
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}
	for _, c := range []struct {
		name, path, key string
		want            int
	}{
		{"no token", "/api/events?topic=jobs", "", 401},
		{"wrong scope", "/api/events?topic=jobs", other, 403},
		{"right scope, allowed topic", "/api/events?topic=jobs", reader, 200},
		{"right scope, refused topic", "/api/events?topic=secret", reader, 403},
	} {
		if got := status(c.path, c.key); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
