// Package ui is the UI contribution manifest a capability module declares, so
// the builder can generate the frontend manifest from the modules a binary
// links. The vocabulary of kinds and regions belongs to the design team; this
// package validates against a Vocabulary it is given and invents no names.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the contribution manifest version. Under Substrate semver
// from v0.1.0; a breaking change bumps it.
const SchemaVersion = 0

// Contribution is one module's UI declaration.
type Contribution struct {
	SchemaVersion int        `json:"schema_version"`
	Module        string     `json:"module"`
	Package       *Package   `json:"package,omitempty"`
	Nav           []NavEntry `json:"nav,omitempty"`
	Settings      []Settings `json:"settings,omitempty"`
	Panels        []Placed   `json:"panels,omitempty"`
	Widgets       []Placed   `json:"widgets,omitempty"`
	Envelopes     *Envelopes `json:"envelopes,omitempty"`
	Routes        []Route    `json:"routes,omitempty"`
}

// Package names the companion TS package. Its version is never written here:
// the BOM's kits section pins it by this name.
type Package struct {
	Name string `json:"name"`
}

// NavEntry is a nav.group or nav.item.
type NavEntry struct {
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group,omitempty"` // nav.item only: key of the group it sits in
	Route string `json:"route,omitempty"` // nav.item only: the frontend route
	Scope string `json:"scope,omitempty"` // scope needed to see the entry
	Order int    `json:"order,omitempty"`
}

// Settings points at a settings group served by the admin module.
type Settings struct {
	ID        string `json:"id"`
	ScopeKind string `json:"scope_kind"` // app | plugin | project | user
}

// Placed is a panel, drawer.tab or widget placed in a region.
type Placed struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Region string `json:"region,omitempty"`
	View   string `json:"view"`
}

// Envelopes lists the envelope kinds a module emits and the actions it accepts.
type Envelopes struct {
	Emits          []string `json:"emits,omitempty"`
	AcceptsActions []string `json:"accepts_actions,omitempty"`
}

// Route is an HTTP endpoint the module serves. Method "*" means any method. A
// Path ending in "/" is a subtree (everything under it), as in net/http. Scope
// is a scope name that may contain "{app}", or the word "authenticated" for
// authentication only. There is deliberately no way to leave it out: an
// unmapped route fails closed.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Scope  string `json:"scope"`
}

// Authenticated is the Scope value meaning any verified caller.
const Authenticated = "authenticated"

// Vocabulary says which kinds each section admits and, optionally, which
// regions exist. An empty Regions list admits any region.
type Vocabulary struct {
	Nav     []string
	Panels  []string
	Widgets []string
	Regions []string
}

// Default is the vocabulary known today: plugin-host-ui's panel, drawer.tab and
// widget, plus the nav kinds the design team has said it will add. Replace it
// when the design team publishes the vocabulary document.
var Default = Vocabulary{
	Nav:     []string{"nav.group", "nav.item"},
	Panels:  []string{"panel", "drawer.tab"},
	Widgets: []string{"widget"},
}

var (
	keyRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	methods  = map[string]bool{"*": true, "GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	scopeKds = map[string]bool{"app": true, "plugin": true, "project": true, "user": true}
	npmName  = regexp.MustCompile(`^@[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)
)

// Parse strictly decodes a contribution (unknown fields are errors).
func Parse(b []byte) (Contribution, error) {
	var c Contribution
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Contribution{}, fmt.Errorf("ui contribution: %w", err)
	}
	return c, nil
}

// MustParse is Parse for embedded ui.json files; a bad one is a programmer error.
func MustParse(b []byte) Contribution {
	c, err := Parse(b)
	if err != nil {
		panic(err)
	}
	return c
}

// Validate returns every problem with c against v.
func Validate(c Contribution, v Vocabulary) []error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("ui %s: "+format, append([]any{c.Module}, a...)...))
	}

	if c.SchemaVersion != SchemaVersion {
		bad("schema_version %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	if !keyRe.MatchString(c.Module) {
		bad("module %q is not a valid name", c.Module)
	}
	if p := c.Package; p != nil {
		if !npmName.MatchString(p.Name) {
			bad("package.name %q must be a scoped npm name such as @scope/name (versions live in the BOM only)", p.Name)
		}
	}
	in := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	region := func(kind, key, r string) {
		if r != "" && len(v.Regions) > 0 && !in(v.Regions, r) {
			bad("%s %q: region %q is not in the vocabulary", kind, key, r)
		}
	}
	// seen checks a key's form and that (kind, key) is unique within a section.
	seen := func(section string) func(kind, key string) {
		keys := map[string]bool{}
		return func(kind, k string) {
			id := kind + "/" + k
			if !keyRe.MatchString(k) {
				bad("%s key %q is not valid", section, k)
			} else if keys[id] {
				bad("duplicate %s key %q", section, k)
			}
			keys[id] = true
		}
	}

	navKey := seen("nav")
	for _, n := range c.Nav {
		navKey(n.Kind, n.Key)
		if !in(v.Nav, n.Kind) {
			bad("nav kind %q is not in the vocabulary", n.Kind)
		}
		if n.Label == "" {
			bad("nav %q needs a label", n.Key)
		}
		if n.Kind == "nav.item" && n.Route == "" {
			bad("nav.item %q needs a route", n.Key)
		}
		if n.Kind == "nav.group" && (n.Route != "" || n.Group != "") {
			bad("nav.group %q cannot carry a route or a parent group", n.Key)
		}
	}
	for _, st := range c.Settings {
		if !keyRe.MatchString(st.ID) {
			bad("settings id %q is not valid", st.ID)
		}
		if !scopeKds[st.ScopeKind] {
			bad("settings %q: scope_kind %q must be app, plugin, project or user", st.ID, st.ScopeKind)
		}
	}
	placed := func(section string, items []Placed, kinds []string) {
		pk := seen(section)
		for _, p := range items {
			pk(p.Kind, p.Key)
			if !in(kinds, p.Kind) {
				bad("%s kind %q is not in the vocabulary", section, p.Kind)
			}
			if p.View == "" {
				bad("%s %q needs a view", section, p.Key)
			}
			region(section, p.Key, p.Region)
		}
	}
	placed("panels", c.Panels, v.Panels)
	placed("widgets", c.Widgets, v.Widgets)

	rk := map[string]bool{}
	for _, r := range c.Routes {
		id := r.Method + " " + r.Path
		if !methods[r.Method] {
			bad("route %q: method must be *, GET, POST, PUT, PATCH or DELETE", id)
		}
		if !strings.HasPrefix(r.Path, "/api/") && !strings.HasPrefix(r.Path, "/admin/") {
			bad("route %q: path must be under /api/ or /admin/", id)
		}
		if r.Path == "/api/" || r.Path == "/admin/" {
			bad("route %q: a subtree must name something under the prefix", id)
		}
		if strings.Contains(r.Path, "..") || strings.ContainsAny(r.Path, "?#") {
			bad("route %q: path must be a plain absolute path", id)
		}
		if r.Scope == "" {
			bad("route %q: scope is required (use %q for authentication only)", id, Authenticated)
		}
		if rk[id] {
			bad("duplicate route %q", id)
		}
		rk[id] = true
	}
	return errs
}
