// Package config is the config module: manifest config values, overridable by a
// YAML file named in SUBSTRATE_CONFIG, exposed per module.
package config

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/substrate/module"
)

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "config" }

func (mod) Start(_ context.Context, app *module.App) error {
	if path := os.Getenv("SUBSTRATE_CONFIG"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var override map[string]map[string]any
		if err := yaml.Unmarshal(b, &override); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for modName, vals := range override {
			cur, _ := app.Config[modName].(map[string]any)
			if cur == nil {
				cur = map[string]any{}
			}
			for k, v := range vals {
				cur[k] = v
			}
			app.Config[modName] = cur
		}
	}
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// String reads a string value from a module's config, or def.
func String(app *module.App, modName, key, def string) string {
	if m, ok := app.Config[modName].(map[string]any); ok {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return def
}
