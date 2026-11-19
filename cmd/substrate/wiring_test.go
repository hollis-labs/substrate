package main

import (
	"reflect"
	"testing"

	"github.com/hollis-labs/substrate/manifest"
)

func TestWiringConfigGivesAServiceTheNotFoundFallback(t *testing.T) {
	withHTTP := []string{"health", "httphost"}
	for _, c := range []struct {
		name    string
		variant string
		mods    []string
		config  map[string]any
		want    any // httphost.fallback in the result, nil for absent
	}{
		{"service", "service", withHTTP, nil, "not_found"},
		{"service keeps other httphost keys", "service", withHTTP, map[string]any{"httphost": map[string]any{"addr": "127.0.0.1:1"}}, "not_found"},
		{"service that says spa", "service", withHTTP, map[string]any{"httphost": map[string]any{"fallback": "spa"}}, "spa"},
		{"service without httphost", "service", []string{"health"}, nil, nil},
		{"app-host", "app-host", withHTTP, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &manifest.Manifest{App: "a", Variant: c.variant, Config: c.config}
			before := reflect.ValueOf(c.config).Len()
			out := wiringConfig(m, c.mods)
			hh, _ := out["httphost"].(map[string]any)
			if got := hh["fallback"]; got != c.want {
				t.Errorf("fallback %v, want %v", got, c.want)
			}
			if c.name == "service keeps other httphost keys" && hh["addr"] != "127.0.0.1:1" {
				t.Errorf("addr lost: %v", hh)
			}
			if reflect.ValueOf(c.config).Len() != before {
				t.Error("the manifest config was changed")
			}
			if c.config != nil {
				if cur, ok := c.config["httphost"].(map[string]any); ok && c.want == "not_found" {
					if _, set := cur["fallback"]; set {
						t.Error("the manifest's httphost map was changed")
					}
				}
			}
		})
	}
}
