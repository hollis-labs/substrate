// Package router is the rule-routing module: libs core/util/router, a small
// declarative matcher that maps the fields of a subject to a target, applies a
// confidence gate, asks a policy hook, and returns a decision shaped to be one
// row of a decision ledger. The app owns its rules and what a target means.
//
// Config under "router":
//
//	rules_file: path to a JSON rule set (see libs router: version, strategy,
//	            rules with conditions, target, priority, auto, min_confidence).
//
// or call router.UseRules(set) before boot.Start. Hooks that cannot be written
// in config (a policy, an escalator, snapshot redaction) go in router.UseConfig.
//
//	d, err := router.Route(ctx, app, librouter.Subject{ID: "msg-1", Fields: map[string]string{"kind": "note"}})
package router

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	librouter "github.com/hollis-labs/libs/core/util/router"

	"github.com/hollis-labs/substrate/module"
)

// Key under which the compiled *librouter.Router is published.
const Key = "router.router"

var (
	mu    sync.Mutex
	rules *librouter.RuleSet
	cfg   librouter.Config
)

// UseRules supplies the rule set in code, replacing rules_file. Call before boot.Start.
func UseRules(rs librouter.RuleSet) { mu.Lock(); rules = &rs; mu.Unlock() }

// UseConfig supplies the router hooks: Policy, PolicyShadow, Escalator, Snapshot.
func UseConfig(c librouter.Config) { mu.Lock(); cfg = c; mu.Unlock() }

// Reset clears the hooks (for tests).
func Reset() { mu.Lock(); rules, cfg = nil, librouter.Config{}; mu.Unlock() }

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "router" }

func (mod) Start(_ context.Context, app *module.App) error {
	c, _ := app.Config["router"].(map[string]any)
	file, _ := c["rules_file"].(string)
	mu.Lock()
	rs, hooks := rules, cfg
	mu.Unlock()

	switch {
	case rs != nil && file != "":
		return errors.New("router: set rules_file or call UseRules, not both")
	case rs == nil && file == "":
		return errors.New("router: needs rules_file in config or a call to router.UseRules")
	case rs == nil:
		b, err := os.ReadFile(file)
		if err != nil {
			return errors.New("router: cannot read rules_file")
		}
		set, err := librouter.ParseRuleSet(b)
		if err != nil {
			return fmt.Errorf("router: rules_file: %w", err)
		}
		rs = &set
	}
	r, err := librouter.Compile(*rs, hooks)
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}
	app.Set(Key, r)
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// From returns the compiled router, or nil.
func From(app *module.App) *librouter.Router {
	v, _ := app.Value(Key)
	r, _ := v.(*librouter.Router)
	return r
}

// Route decides where a subject goes.
func Route(ctx context.Context, app *module.App, s librouter.Subject) (librouter.Decision, error) {
	r := From(app)
	if r == nil {
		return librouter.Decision{}, errors.New("router module is not started")
	}
	return r.Route(ctx, s)
}
