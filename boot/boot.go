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

// Start starts modules in order. configJSON is the manifest config block.
func Start(ctx context.Context, name string, modules []string, configJSON string) (*Running, error) {
	cfg := map[string]any{}
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("boot: config: %w", err)
		}
	}
	app := &module.App{Name: name, Config: cfg, Values: map[string]any{}}
	r := &Running{App: app}
	if err := r.rt.Start(ctx, app, modules); err != nil {
		return nil, err
	}
	return r, nil
}

// Stop stops modules in reverse order.
func (r *Running) Stop(ctx context.Context) error { return r.rt.Stop(ctx) }
