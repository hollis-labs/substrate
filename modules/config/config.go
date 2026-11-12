// Package config is the config module: manifest config values, overridable by a
// YAML file named in SUBSTRATE_CONFIG, exposed per module.
//
// The override file is a map of module name to that module's config block. Each
// block is deep-merged over the manifest's block for that module:
//
//   - maps merge recursively, key by key; keys only in the manifest stay;
//   - scalars and lists replace the manifest value whole (lists are not merged
//     element by element);
//   - a key present only in the override is added;
//   - an explicit null removes the key from the merged result, at any depth, so
//     the module falls back to its own default for it. A null for a key the
//     manifest does not have is a no-op. A null module block (`database: null`)
//     changes nothing;
//   - a type clash is resolved for the override: a scalar or list replaces a
//     map, and a map replaces a scalar or list (nulls inside that map are
//     dropped, as above);
//   - a module block that is not a map (`database: 3`) is an error.
//
// Before this module deep-merged, the merge was one level deep and a nested
// block in the override replaced the whole manifest block.
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
		var override map[string]any
		if err := yaml.Unmarshal(b, &override); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for modName, v := range override {
			if v == nil {
				continue
			}
			vals, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("parse %s: config for %q must be a map, got %T", path, modName, v)
			}
			cur, _ := app.Config[modName].(map[string]any)
			app.Config[modName] = merge(cur, vals)
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

// merge returns base with over deep-merged on top; neither input is modified.
func merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		switch ov := v.(type) {
		case nil:
			delete(out, k)
		case map[string]any:
			bm, _ := out[k].(map[string]any)
			out[k] = merge(bm, ov)
		default:
			out[k] = v
		}
	}
	return out
}
