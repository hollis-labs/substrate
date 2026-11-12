// Package ui is the UI contribution manifest a capability module declares, so
// the builder can generate the frontend manifest from the modules a binary
// links. The kinds and regions are the design team's published vocabulary
// (SF-2, v0.4: v0.3 plus menu.item and command); this package validates against
// them and invents no names.
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
	// surface, panel, dock.tab, widget, slot, menu.item and command.
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

	// menu.item and command (v0.4)
	Action *Action `json:"action,omitempty"`
	Risk   string  `json:"risk,omitempty"` // low (default) | medium | high

	// menu.item
	Order int    `json:"order,omitempty"` // lower first; default 100
	When  string `json:"when,omitempty"`  // a subject kind; only in a menu:<subject-kind> region

	// command
	Keywords []string `json:"keywords,omitempty"`
	Group    string   `json:"group,omitempty"` // the palette section label
}

// Action is what a menu.item or command does. Exactly one of Navigate,
// OpenRegion and Call is set. It carries no code and no URL: the host sends a
// Call through its action gateway, which applies risk and confirmation policy.
type Action struct {
	// Navigate is the path of a registered frontend route (a surface's route),
	// never a URL and never a backend path.
	Navigate string `json:"navigate,omitempty"`
	// OpenRegion is a docked region to open; Tab optionally names a dock.tab in it.
	OpenRegion string `json:"openRegion,omitempty"`
	Tab        string `json:"tab,omitempty"`
	// Call is a catalog function name the gateway executes; Input is its argument object.
	Call  string         `json:"call,omitempty"`
	Input map[string]any `json:"input,omitempty"`
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

// Public is the Scope value meaning no authentication at all, for the few routes
// that must work before a caller has an identity: first-run setup, login, a
// published key set, pairing. A public route is reachable in every auth mode, so
// its handler is responsible for everything it allows.
const Public = "public"

// Fixed region names published by hosts.
const (
	RegionNavPrimary       = "nav.primary"
	RegionNavFooter        = "nav.footer"
	RegionNavGroupPrefix   = "nav.group:"
	RegionSettingsSections = "settings.sections"
	RegionSurfacePage      = "surface.page"
	RegionAsidePanels      = "aside.panels"
	RegionWidgetsGrid      = "widgets.grid"

	// v0.4: command and menu regions. Core, in every host, not declared.
	RegionCommandPalette = "command.palette"
	RegionMenuPrefix     = "menu:" // menu:<subject-kind>, e.g. menu:plugin
	RegionRowActions     = "row.actions"
	RegionToolbarPrimary = "toolbar.primary"
)

// DefaultMenuOrder is the order of a menu.item that names none.
const DefaultMenuOrder = 100

// Vocabulary is the set of kinds a host accepts and, optionally, the regions it
// publishes. An empty Regions list admits any host-defined region (docked
// regions and inline slots are named by the host, not by this package).
type Vocabulary struct {
	Kinds   []string
	Regions []string
}

// Default is the vocabulary: v0.3 (approved by the owner on 2026-11-05) plus the
// v0.4 delta (menu.item and command, drafted by the Interaction Designer). dock.tab
// replaced drawer.tab with no alias; plugin-host-ui 0.3.0 renamed its kind to match.
var Default = Vocabulary{
	Kinds: []string{"nav.item", "nav.group", "settings.region", "surface", "panel", "dock.tab", "widget", "slot", "menu.item", "command"},
}

var (
	keyRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	methods  = map[string]bool{"*": true, "GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	scopeKds = map[string]bool{"app": true, "plugin": true, "project": true, "user": true}
	layouts  = map[string]bool{"list": true, "detail": true, "form": true, "settings": true, "dashboard": true}
	npmName  = regexp.MustCompile(`^@[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)
	risks    = map[string]bool{"low": true, "medium": true, "high": true}
	// callRe is a catalog function name; they are camelCase (openOverlay) or dotted.
	callRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	// emitRe is an envelope kind with its version: kind@version.
	emitRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9_.+-]*$`)
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
	case "command":
		return RegionCommandPalette
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

	if e := c.Envelopes; e != nil {
		for _, k := range e.Emits {
			if !emitRe.MatchString(k) {
				bad("envelopes.emits %q must be kind@version", k)
			}
		}
	}

	seen := map[string]bool{}
	for _, it := range c.Contributions {
		id := it.Kind + "/" + it.Key
		if it.Kind == "menu.item" {
			id += "@" + it.EffectiveRegion() // unique per (region, module, key)
		}
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
		case "menu.item", "command":
			if it.Label == "" {
				bad("needs a label")
			}
			if it.Risk != "" && !risks[it.Risk] {
				bad("risk %q must be low, medium or high", it.Risk)
			}
			if it.Order < 0 {
				bad("order %d must not be negative", it.Order)
			}
			if it.When != "" && (!strings.HasPrefix(region, RegionMenuPrefix) || !keyRe.MatchString(it.When)) {
				bad("when %q applies only to a menu:<subject-kind> region and must be a subject kind", it.When)
			}
			checkAction(it.Action, bad)
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
	"menu.item":       {"action", "risk", "order", "when", "shortcut"},
	"command":         {"action", "risk", "keywords", "group"},
}

// setFields reports which kind-specific fields the item sets.
func (it Item) setFields() map[string]bool {
	return map[string]bool{
		"view": it.View != "", "route": it.Route != "", "badge": it.Badge != "", "requires": it.Requires != "",
		"collapsible": it.Collapsible != nil, "default_open": it.DefaultOpen,
		"section_id": it.SectionID != "", "groups": len(it.Groups) > 0,
		"layout": it.Layout != "", "title": it.Title != "",
		"shortcut": it.Shortcut != "", "placements": len(it.Placements) > 0, "opens": it.Opens != "",
		"action": it.Action != nil, "risk": it.Risk != "", "order": it.Order != 0, "when": it.When != "",
		"keywords": len(it.Keywords) > 0, "group": it.Group != "",
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
	case "command":
		return region == RegionCommandPalette
	case "menu.item":
		switch {
		case region == RegionRowActions || region == RegionToolbarPrimary:
			return true
		case strings.HasPrefix(region, RegionMenuPrefix):
			return keyRe.MatchString(strings.TrimPrefix(region, RegionMenuPrefix))
		case region == RegionCommandPalette:
			return false
		}
		// A host-defined, non-docked region may also hold a menu.item; whether the
		// host declares it, and that it is not docked, is checked when the
		// contributions are merged.
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

// checkAction validates the shared action reference of a menu.item or command.
// Whether a navigate target is a registered route, and a region and tab exist,
// is checked when contributions from every selected module are merged.
func checkAction(a *Action, bad func(string, ...any)) {
	if a == nil {
		bad("needs an action")
		return
	}
	set := 0
	for _, s := range []string{a.Navigate, a.OpenRegion, a.Call} {
		if s != "" {
			set++
		}
	}
	if set != 1 {
		bad("action must set exactly one of navigate, openRegion and call")
		return
	}
	switch {
	case a.Navigate != "":
		frontendRoute(a.Navigate, bad)
		if a.Tab != "" || a.Input != nil {
			bad("action navigate takes no tab or input")
		}
	case a.OpenRegion != "":
		if !keyRe.MatchString(a.OpenRegion) {
			bad("action openRegion %q is not a region name", a.OpenRegion)
		}
		if a.Tab != "" && !keyRe.MatchString(a.Tab) {
			bad("action tab %q is not a dock.tab key", a.Tab)
		}
		if a.Input != nil {
			bad("action openRegion takes no input")
		}
	case a.Call != "":
		if !callRe.MatchString(a.Call) {
			bad("action call %q is not a catalog function name", a.Call)
		}
		if a.Tab != "" {
			bad("action call takes no tab")
		}
	}
}
