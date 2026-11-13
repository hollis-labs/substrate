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
// Numbers: YAML decodes a whole number to an int, and the manifest's generated
// JSON gives float64, so the merge converts every integer from the override
// file (int, int8..int64, uint..uint64) to float64 as it merges, at any depth,
// inside nested maps and lists. A module therefore reads a number one way,
// `.(float64)`, whichever source set it. A non-integer float stays as it is.
// An integer whose magnitude is above 2^53 does not fit float64 exactly and is
// refused with an error naming the key, never rounded. Booleans and strings are
// untouched.
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
			merged, err := merge(cur, vals, modName)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			app.Config[modName] = merged
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

// maxExactInt is 2^53, the largest magnitude at which every integer is exactly
// representable as a float64.
const maxExactInt = 1 << 53

// merge returns base with over deep-merged on top; neither input is modified.
// Integers in over become float64. at is the dotted path of the block, for errors.
func merge(base, over map[string]any, at string) (map[string]any, error) {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		path := at + "." + k
		switch ov := v.(type) {
		case nil:
			delete(out, k)
		case map[string]any:
			bm, _ := out[k].(map[string]any)
			m, err := merge(bm, ov, path)
			if err != nil {
				return nil, err
			}
			out[k] = m
		default:
			n, err := normalize(v, path)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
	}
	return out, nil
}

// normalize returns v with every integer converted to float64, recursing into
// maps and lists. It copies the containers; v is not modified.
func normalize(v any, path string) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			n, err := normalize(e, path+"."+k)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			n, err := normalize(e, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case int:
		return exact(int64(x), path)
	case int8:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return exact(x, path)
	case uint:
		return exactU(uint64(x), path)
	case uint8:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return exactU(x, path)
	}
	return v, nil
}

func exact(n int64, path string) (any, error) {
	if n > maxExactInt || n < -maxExactInt {
		return nil, fmt.Errorf("config %q: integer %d does not fit a number exactly (limit 2^53)", path, n)
	}
	return float64(n), nil
}

func exactU(n uint64, path string) (any, error) {
	if n > maxExactInt {
		return nil, fmt.Errorf("config %q: integer %d does not fit a number exactly (limit 2^53)", path, n)
	}
	return float64(n), nil
}
