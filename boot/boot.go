// Package boot starts the modules a generated wiring file selected.
package boot

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/substrate/module"
)

// Running is a started app.
type Running struct {
	App *module.App
	rt  module.Runtime
}

// Hook is app code run during start; see Options.
type Hook = module.Hook

// Options are the boot hooks of StartWith.
type Options struct {
	// AfterConfig runs once, with the app, right after the config module has
	// applied the SUBSTRATE_CONFIG override and before any later module starts.
	// Use it to set run-time values (resolve relative paths against a data
	// directory). If the config module is not selected there is no override, and
	// it runs once before the first module starts.
	AfterConfig Hook
	// Before maps a module name to a hook run immediately before that module's
	// Start (for example a guard keyed to "database", before it migrates). A
	// name not in the start list is an error at start.
	Before map[string]Hook
}

// Start starts modules in order. configJSON is the manifest config block.
func Start(ctx context.Context, name string, modules []string, configJSON string) (*Running, error) {
	return StartWith(ctx, name, modules, configJSON, Options{})
}

// StartWith is Start with boot hooks. A hook error stops the boot and stops the
// modules already started, in reverse order, as a module error does.
func StartWith(ctx context.Context, name string, modules []string, configJSON string, opts Options) (*Running, error) {
	cfg := map[string]any{}
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("boot: config: %w", err)
		}
	}
	if cfg == nil { // a manifest with no config block generates "null", which decodes to a nil map
		cfg = map[string]any{}
	}
	app := &module.App{Name: name, Config: cfg, Values: map[string]any{}}
	r := &Running{App: app}
	if err := r.rt.StartWith(ctx, app, modules, module.Hooks{AfterConfig: opts.AfterConfig, Before: opts.Before}); err != nil {
		return nil, err
	}
	return r, nil
}

// Stop stops modules in reverse order.
func (r *Running) Stop(ctx context.Context) error { return r.rt.Stop(ctx) }
