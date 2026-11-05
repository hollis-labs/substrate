// Package ui is the UI contribution manifest a capability module declares, so
// the builder can generate the frontend manifest from the modules a binary
// links. The kinds and regions are the design team's published vocabulary
// (SF-2, v0.3); this package validates against them and invents no names.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the contribution manifest version. Under Substrate semver;
// a breaking change bumps it.
const SchemaVersion = 0

// Contribution is one module's UI declaration.
type Contribution struct {
	SchemaVersion int      `json:"schema_version"`
	Module        string   `json:"module"`
	Package       *Package `json:"package,omitempty"`
	// Contributions are the UI pieces: nav.item, nav.group, settings.region,
	// surface, panel, dock.tab, widget and slot.
	Contributions []Item     `json:"contributions,omitempty"`
	Envelopes     *Envelopes `json:"envelopes,omitempty"`
	Routes        []Route    `json:"routes,omitempty"`
}

// Package names the companion TS package. Its version is never written here:
// the BOM's kits section pins it by this name.
type Package struct {
	Name string `json:"name"`
}

// Item is one contribution. The fields that apply depend on Kind; Validate
// rejects a field on a kind that does not take it.
type Item struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Label    string `json:"label,omitempty"`
	Icon     string `json:"icon,omitempty"` // a kit icon name
	Priority int    `json:"priority,omitempty"`
	Region   string `json:"region,omitempty"`

	// panel, dock.tab, widget: the host component that renders it.
	View string `json:"view,omitempty"`

	// nav.item
	Route    string `json:"route,omitempty"`    // also surface
	Badge    string `json:"badge,omitempty"`    // a key the host resolves to a count
	Requires string `json:"requires,omitempty"` // advisory scope hint; the backend authorizes

	// nav.group
	Collapsible *bool `json:"collapsible,omitempty"` // default true
	DefaultOpen bool  `json:"default_open,omitempty"`

	// settings.region: groups are served by the admin manifest, never defined here.
	SectionID string     `json:"section_id,omitempty"`
	Groups    []GroupRef `json:"groups,omitempty"`

	// surface
	Layout string `json:"layout,omitempty"` // list | detail | form | settings | dashboard
	Title  string `json:"title,omitempty"`

	// dock.tab
	Shortcut   string   `json:"shortcut,omitempty"`
	Placements []string `json:"placements,omitempty"`

	// slot: the key of a dock.tab this slot toggles.
	Opens string `json:"opens,omitempty"`
}

// GroupRef names an admin-manifest settings group and its scope.
type GroupRef struct {
	ID        string `json:"id"`
	ScopeKind string `json:"scope_kind"` // app | plugin | project | user
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

// Fixed region names published by hosts.
const (
	RegionNavPrimary       = "nav.primary"
	RegionNavFooter        = "nav.footer"
	RegionNavGroupPrefix   = "nav.group:"
	RegionSettingsSections = "settings.sections"
	RegionSurfacePage      = "surface.page"
	RegionAsidePanels      = "aside.panels"
	RegionWidgetsGrid      = "widgets.grid"
)

// Vocabulary is the set of kinds a host accepts and, optionally, the regions it
// publishes. An empty Regions list admits any host-defined region (docked
// regions and inline slots are named by the host, not by this package).
type Vocabulary struct {
	Kinds   []string
	Regions []string
}

// Default is the vocabulary v0.3 approved by the owner on 2026-11-05. Note
// dock.tab: the design-kit's plugin-host-ui 0.2.0 still exports it as
// drawer.tab; a host maps the name until the kit renames it.
var Default = Vocabulary{
	Kinds: []string{"nav.item", "nav.group", "settings.region", "surface", "panel", "dock.tab", "widget", "slot"},
}

var (
	keyRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	methods  = map[string]bool{"*": true, "GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	scopeKds = map[string]bool{"app": true, "plugin": true, "project": true, "user": true}
	layouts  = map[string]bool{"list": true, "detail": true, "form": true, "settings": true, "dashboard": true}
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

// DefaultRegion is where a kind goes when the item names no region; "" means
// the host defines it and the item must say.
func DefaultRegion(kind string) string {
	switch kind {
	case "nav.item", "nav.group":
		return RegionNavPrimary
	case "settings.region":
		return RegionSettingsSections
	case "surface":
		return RegionSurfacePage
	case "panel":
		return RegionAsidePanels
	case "widget":
		return RegionWidgetsGrid
	}
	return ""
}

// EffectiveRegion returns the item's region, or the kind's default.
func (it Item) EffectiveRegion() string {
	if it.Region != "" {
		return it.Region
	}
	return DefaultRegion(it.Kind)
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
	if p := c.Package; p != nil && !npmName.MatchString(p.Name) {
		bad("package.name %q must be a scoped npm name such as @scope/name (versions live in the BOM only)", p.Name)
	}
	in := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}

	seen := map[string]bool{}
	for _, it := range c.Contributions {
		id := it.Kind + "/" + it.Key
		switch {
		case !in(v.Kinds, it.Kind):
			bad("kind %q is not in the vocabulary", it.Kind)
			continue
		case !keyRe.MatchString(it.Key):
			bad("%s key %q is not valid", it.Kind, it.Key)
		case seen[id]:
			bad("duplicate %s %q", it.Kind, it.Key)
		}
		seen[id] = true
		bad := func(format string, a ...any) { bad(it.Kind+" "+it.Key+": "+format, a...) }

		// Which fields each kind takes. Anything else is refused rather than ignored.
		allowed := map[string]bool{"kind": true, "key": true, "label": true, "icon": true, "priority": true, "region": true}
		for _, f := range kindFields[it.Kind] {
			allowed[f] = true
		}
		for f, set := range it.setFields() {
			if set && !allowed[f] {
				bad("field %q does not apply", f)
			}
		}

		region := it.EffectiveRegion()
		if region == "" {
			bad("region is required: the host names it")
		} else if !regionOK(it, region, v) {
			bad("region %q is not allowed for this kind", region)
		}

		switch it.Kind {
		case "nav.item":
			if it.Label == "" {
				bad("needs a label")
			}
			frontendRoute(it.Route, bad)
		case "nav.group":
			if it.Label == "" {
				bad("needs a label")
			}
		case "settings.region":
			if it.SectionID == "" || it.Label == "" {
				bad("needs section_id and label")
			}
			if len(it.Groups) == 0 {
				bad("needs at least one group")
			}
			for _, g := range it.Groups {
				if !keyRe.MatchString(g.ID) {
					bad("group id %q is not valid", g.ID)
				}
				if !scopeKds[g.ScopeKind] {
					bad("group %q: scope_kind %q must be app, plugin, project or user", g.ID, g.ScopeKind)
				}
			}
		case "surface":
			frontendRoute(it.Route, bad)
			if !layouts[it.Layout] {
				bad("layout %q must be list, detail, form, settings or dashboard", it.Layout)
			}
			if it.Title == "" {
				bad("needs a title")
			}
		case "panel", "dock.tab", "widget":
			if it.View == "" {
				bad("needs a view")
			}
		case "slot":
			// opens is optional; whether it names a real dock.tab is checked when
			// contributions from every selected module are merged.
		}
	}

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

// kindFields lists the kind-specific fields each kind takes.
var kindFields = map[string][]string{
	"nav.item":        {"route", "badge", "requires"},
	"nav.group":       {"collapsible", "default_open"},
	"settings.region": {"section_id", "groups"},
	"surface":         {"route", "layout", "title"},
	"panel":           {"view"},
	"dock.tab":        {"view", "shortcut", "placements"},
	"widget":          {"view"},
	"slot":            {"opens"},
}

// setFields reports which kind-specific fields the item sets.
func (it Item) setFields() map[string]bool {
	return map[string]bool{
		"view": it.View != "", "route": it.Route != "", "badge": it.Badge != "", "requires": it.Requires != "",
		"collapsible": it.Collapsible != nil, "default_open": it.DefaultOpen,
		"section_id": it.SectionID != "", "groups": len(it.Groups) > 0,
		"layout": it.Layout != "", "title": it.Title != "",
		"shortcut": it.Shortcut != "", "placements": len(it.Placements) > 0, "opens": it.Opens != "",
	}
}

// regionOK says whether region may hold this kind.
func regionOK(it Item, region string, v Vocabulary) bool {
	switch it.Kind {
	case "nav.item":
		return region == RegionNavPrimary || region == RegionNavFooter ||
			(strings.HasPrefix(region, RegionNavGroupPrefix) && keyRe.MatchString(strings.TrimPrefix(region, RegionNavGroupPrefix)))
	case "nav.group":
		return region == RegionNavPrimary
	case "settings.region":
		return region == RegionSettingsSections
	case "surface":
		return region == RegionSurfacePage
	case "panel":
		return region == RegionAsidePanels
	case "widget":
		return region == RegionWidgetsGrid
	}
	// dock.tab and slot go in host-defined regions (a docked region, an inline
	// slot). When the host publishes its regions, hold the item to them.
	if len(v.Regions) == 0 {
		return keyRe.MatchString(region)
	}
	for _, r := range v.Regions {
		if r == region {
			return true
		}
	}
	return false
}

// frontendRoute checks a frontend route: a path in the SPA, never a backend path.
func frontendRoute(route string, bad func(string, ...any)) {
	switch {
	case route == "" || !strings.HasPrefix(route, "/"):
		bad("route %q must be an absolute frontend path", route)
	case strings.HasPrefix(route, "/api/"):
		bad("route %q is a backend path: frontend routes must never equal an API path", route)
	case strings.Contains(route, "..") || strings.ContainsAny(route, "?#"):
		bad("route %q must be a plain path", route)
	}
}
