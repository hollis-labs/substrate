// Package policy is the authorization module: libs security/policy's evaluator
// with audit to slog. Substrate supplies the boundary; the app supplies the
// rules by registering a PDP with UsePDP before boot.Start. Substrate never
// owns policy, so starting this module without a PDP is an error rather than a
// silent allow.
//
// Config under "policy":
//
//	mode:      shadow (default, observe: allow at this boundary, audit the
//	           decision) or enforce.
//	fail_mode: closed (default) or open_with_audit.
package policy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	libpolicy "github.com/hollis-labs/libs/security/policy"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/logging"
)

// Key under which the *Authorizer is published.
const Key = "policy.authorizer"

var (
	mu  sync.Mutex
	pdp libpolicy.PDP
)

// UsePDP registers the app's decision point. Call it before boot.Start.
func UsePDP(p libpolicy.PDP) {
	mu.Lock()
	defer mu.Unlock()
	pdp = p
}

// AllowAll is an explicit development PDP that allows every request. It is
// never a default: an app must name it.
type AllowAll struct{}

func (AllowAll) Evaluate(context.Context, libpolicy.Request) (libpolicy.Decision, error) {
	return libpolicy.Decision{Effect: libpolicy.Allow, Reason: "allow-all development PDP"}, nil
}

// Authorizer evaluates requests under the configured mode.
type Authorizer struct {
	ev   libpolicy.Evaluator
	opts libpolicy.Options
}

// Evaluate returns the admission for req.
func (a *Authorizer) Evaluate(ctx context.Context, req libpolicy.Request) (libpolicy.Result, error) {
	return a.ev.Evaluate(ctx, req, a.opts)
}

type slogSink struct{ l *slog.Logger }

func (s slogSink) Record(ctx context.Context, e libpolicy.AuditEvent) error {
	s.l.LogAttrs(ctx, slog.LevelInfo, "policy",
		slog.String("action", e.Action), slog.String("resource", e.Resource.Type+":"+e.Resource.ID),
		slog.String("principal", e.Principal.URN), slog.String("effect", string(e.Decision.Effect)),
		slog.String("admission", string(e.ProposedAdmission)), slog.Bool("would_block", e.WouldBlock),
		slog.String("mode", string(e.Mode)), slog.String("failure", string(e.Failure)))
	return nil
}

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "policy" }

func (mod) Start(_ context.Context, app *module.App) error {
	mu.Lock()
	p := pdp
	mu.Unlock()
	if p == nil {
		return fmt.Errorf("no PDP registered: call policy.UsePDP before boot.Start (policy.AllowAll{} is available for development)")
	}
	cfg, _ := app.Config["policy"].(map[string]any)
	str := func(k, def string) string {
		if s, ok := cfg[k].(string); ok && s != "" {
			return s
		}
		return def
	}
	opts := libpolicy.Options{
		Mode:     libpolicy.Mode(str("mode", string(libpolicy.Shadow))),
		FailMode: libpolicy.FailMode(str("fail_mode", string(libpolicy.FailClosed))),
	}
	if err := opts.Validate(); err != nil {
		return err
	}
	l := slog.Default()
	if v, ok := app.Value(logging.Key); ok {
		l, _ = v.(*slog.Logger)
	}
	app.Set(Key, &Authorizer{ev: libpolicy.Evaluator{PDP: p, Audit: slogSink{l}}, opts: opts})
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// From returns the authorizer published by the policy module, or nil.
func From(app *module.App) *Authorizer {
	v, _ := app.Value(Key)
	a, _ := v.(*Authorizer)
	return a
}
