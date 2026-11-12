// Package manifest parses and validates substrate.yaml.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/substrate/catalog"
)

type Manifest struct {
	App      string         `yaml:"app"`
	Variant  string         `yaml:"variant"`
	Profiles []string       `yaml:"profiles"`
	Modules  Modules        `yaml:"modules"`
	Config   map[string]any `yaml:"config"`
	Frontend Frontend       `yaml:"frontend"`
}

// Frontend is what the app's host declares for the generated frontend manifest.
type Frontend struct {
	// Contributions is the app's own UI contribution file, a path relative to
	// the manifest file. It has the shape of a module's ui.json with `module`
	// equal to the app name, and is merged with the selected modules' items
	// under the same rules. It may carry contributions, package and envelopes,
	// not routes (an app mounts its routes through httphost).
	Contributions string `yaml:"contributions"`
	// Regions is the host region catalog: the places a dock.tab or slot may
	// contribute to. The core regions (nav.primary, nav.group:<key>, nav.footer,
	// settings.sections, surface.page, aside.panels, widgets.grid) exist in every
	// host and are not declared. Anything else is host-defined and must be listed.
	Regions []Region `yaml:"regions"`
}

// Region is one host-defined region. Its JSON form (in the generated frontend
// manifest) is snake_case; the dock attributes are optional and apply to docked
// regions only. A docked region that sets none gets the vocabulary defaults.
type Region struct {
	Name string `yaml:"name" json:"name"`
	// Docked marks a region that holds dock.tab contributions and opens and closes
	// (a drawer). A docked region must name the inline region it is tucked under.
	Docked bool `yaml:"docked" json:"docked"`
	// Anchor is the inline region a docked region is tucked under; the slots in
	// that region are the triggers that open it.
	Anchor string `yaml:"anchor" json:"anchor,omitempty"`

	// OpenState is "hidden" (default) or "open": whether the region starts with its first tab showing.
	OpenState string `yaml:"open_state" json:"open_state,omitempty"`
	// Exclusive: one tab open at a time. Default false, true for a bottom dock.
	Exclusive *bool `yaml:"exclusive" json:"exclusive,omitempty"`
	// Sizes are the size tokens (sm, md, lg) a user may pick from, and the default.
	Sizes *DockSizes `yaml:"sizes" json:"sizes,omitempty"`
	// DismissKey closes the region; "Esc" (default).
	DismissKey string `yaml:"dismiss_key" json:"dismiss_key,omitempty"`
	// Mobile is how the region presents on a narrow screen: "bottom-sheet" (default).
	Mobile string `yaml:"mobile" json:"mobile,omitempty"`
	// Edge is where the dock sits: "side" (default, a right-hand panel) or "bottom"
	// (a resizable bottom dock). It is not Anchor, which names an inline region.
	Edge string `yaml:"edge" json:"edge,omitempty"`
	// HeightPx bounds a bottom dock's height in pixels; only with Edge "bottom".
	HeightPx *DockHeight `yaml:"height_px" json:"height_px,omitempty"`
}

// DockSizes are the size tokens a docked region offers.
type DockSizes struct {
	Options []string `yaml:"options" json:"options"`
	Default string   `yaml:"default" json:"default"`
}

// DockHeight bounds a bottom dock: min <= default <= max, all positive. A zero
// field means the shell's default for it (120, 240, 640).
type DockHeight struct {
	Min     int `yaml:"min" json:"min,omitempty"`
	Default int `yaml:"default" json:"default,omitempty"`
	Max     int `yaml:"max" json:"max,omitempty"`
}

// hasDockAttributes reports whether the region sets any dock attribute.
func (r Region) hasDockAttributes() bool {
	return r.OpenState != "" || r.Exclusive != nil || r.Sizes != nil || r.DismissKey != "" || r.Mobile != "" || r.Edge != "" || r.HeightPx != nil
}

type Modules struct {
	Add    []string `yaml:"add"`
	Remove []string `yaml:"remove"`
}

// Load reads and strictly parses a manifest file.
func Load(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse strictly parses manifest bytes: unknown fields are errors.
func Parse(b []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &m, nil
}

// Resolve returns the ordered, de-duplicated module list: profiles in order,
// then add, minus remove. Order is dependency-safe (Requires first).
func (m *Manifest) Resolve() ([]string, error) {
	if errs := m.Validate(); len(errs) > 0 {
		return nil, errs[0]
	}
	seen := map[string]bool{}
	var out []string
	var add func(string)
	add = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		if mod, ok := catalog.Find(n); ok {
			for _, r := range mod.Requires {
				add(r)
			}
		}
		out = append(out, n)
	}
	removed := map[string]bool{}
	for _, r := range m.Modules.Remove {
		removed[r] = true
	}
	var want []string
	for _, p := range m.Profiles {
		want = append(want, catalog.Profiles[p]...)
	}
	want = append(want, m.Modules.Add...)
	for _, n := range want {
		if !removed[n] {
			add(n)
		}
	}
	// The variant's needs are mandatory: removing one is an error below.
	if v, ok := catalog.Variants[m.Variant]; ok {
		for _, n := range v.Needs {
			add(n)
		}
	}
	// Requirements may re-introduce a removed module; that is a validation error.
	for _, n := range out {
		if removed[n] {
			return nil, fmt.Errorf("manifest: %q is removed but required by another selected module or the variant", n)
		}
	}
	return out, nil
}

// Validate checks the manifest against the catalogue and returns every problem.
func (m *Manifest) Validate() []error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("manifest: "+f, a...)) }
	if strings.TrimSpace(m.App) == "" {
		bad("app is required")
	}
	if _, ok := catalog.Variants[m.Variant]; !ok {
		bad("unknown variant %q (known: %s)", m.Variant, strings.Join(keys(catalog.Variants), ", "))
	}
	if len(m.Profiles) == 0 {
		bad("at least one profile is required")
	}
	for _, p := range m.Profiles {
		if _, ok := catalog.Profiles[p]; !ok {
			bad("unknown profile %q (known: %s)", p, strings.Join(keys(catalog.Profiles), ", "))
		}
	}
	for _, list := range [][]string{m.Modules.Add, m.Modules.Remove} {
		for _, n := range list {
			if _, ok := catalog.Find(n); !ok {
				bad("unknown module %q", n)
			}
		}
	}
	for k := range m.Config {
		if _, ok := catalog.Find(k); !ok && k != m.App {
			bad("config for unknown module %q (the app's own block is config.%s)", k, m.App)
		}
	}
	if c := m.Frontend.Contributions; c != "" {
		if _, ok := catalog.Find(m.App); ok {
			bad("frontend.contributions: app name %q is a catalogue module; an app contributes under its own name", m.App)
		}
		if filepath.IsAbs(c) || !filepath.IsLocal(filepath.FromSlash(c)) {
			bad("frontend.contributions %q must be a path inside the manifest's directory", c)
		}
	}
	errs = append(errs, m.checkRegions()...)
	if len(errs) > 0 {
		return errs
	}
	// Selected modules must be buildable.
	return append(errs, m.checkSelected()...)
}

func (m *Manifest) checkSelected() []error {
	var errs []error
	removed := map[string]bool{}
	for _, r := range m.Modules.Remove {
		removed[r] = true
	}
	sel := map[string]bool{}
	for _, p := range m.Profiles {
		for _, n := range catalog.Profiles[p] {
			sel[n] = true
		}
	}
	for _, n := range m.Modules.Add {
		sel[n] = true
	}
	for n := range sel {
		if removed[n] {
			continue
		}
		if mod, _ := catalog.Find(n); mod.Status != "ready" {
			errs = append(errs, fmt.Errorf("manifest: module %q is %s and cannot be built yet", n, mod.Status))
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// coreRegion reports whether name is one of the regions every host has.
func coreRegion(name string) bool {
	switch name {
	case "nav.primary", "nav.footer", "settings.sections", "surface.page", "aside.panels", "widgets.grid",
		"command.palette", "row.actions", "toolbar.primary":
		return true
	}
	return strings.HasPrefix(name, "nav.group:") || strings.HasPrefix(name, "menu:")
}

// checkRegions validates the host region catalog.
func (m *Manifest) checkRegions() []error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("manifest: frontend.regions: "+f, a...)) }
	byName := map[string]Region{}
	for _, r := range m.Frontend.Regions {
		switch {
		case coreRegion(r.Name):
			bad("%q is a core region and is not declared", r.Name)
			continue
		case !regionName.MatchString(r.Name):
			bad("%q is not a valid region name", r.Name)
			continue
		}
		if _, dup := byName[r.Name]; dup {
			bad("%q is declared twice", r.Name)
		}
		byName[r.Name] = r
	}
	for _, r := range byName {
		errs = append(errs, checkDockAttributes(r)...)
		switch {
		case r.Docked && r.Anchor == "":
			bad("docked region %q must declare its anchor", r.Name)
		case !r.Docked && r.Anchor != "":
			bad("region %q has an anchor but is not docked", r.Name)
		case r.Docked:
			a, ok := byName[r.Anchor]
			switch {
			case !ok:
				bad("docked region %q is anchored to %q, which is not declared", r.Name, r.Anchor)
			case a.Docked:
				bad("docked region %q is anchored to the docked region %q; an anchor is an inline region", r.Name, r.Anchor)
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// checkDockAttributes validates the dock attributes of one region.
func checkDockAttributes(r Region) []error {
	var errs []error
	bad := func(f string, a ...any) {
		errs = append(errs, fmt.Errorf("manifest: frontend.regions: region %q: "+f, append([]any{r.Name}, a...)...))
	}
	if !r.hasDockAttributes() {
		return nil
	}
	if !r.Docked {
		bad("dock attributes apply to docked regions only")
		return errs
	}
	if r.OpenState != "" && r.OpenState != "hidden" && r.OpenState != "open" {
		bad("open_state %q must be hidden or open", r.OpenState)
	}
	if r.DismissKey != "" && r.DismissKey != "Esc" {
		bad("dismiss_key %q must be Esc", r.DismissKey)
	}
	if r.Mobile != "" && r.Mobile != "bottom-sheet" {
		bad("mobile %q must be bottom-sheet", r.Mobile)
	}
	if r.Edge != "" && r.Edge != "side" && r.Edge != "bottom" {
		bad("edge %q must be side or bottom", r.Edge)
	}
	if r.Sizes != nil {
		size := map[string]bool{"sm": true, "md": true, "lg": true}
		seen := map[string]bool{}
		for _, o := range r.Sizes.Options {
			if !size[o] {
				bad("sizes.options has %q; sizes are sm, md and lg", o)
			}
			if seen[o] {
				bad("sizes.options lists %q twice", o)
			}
			seen[o] = true
		}
		switch {
		case len(r.Sizes.Options) == 0:
			bad("sizes.options must list at least one size")
		case !seen[r.Sizes.Default]:
			bad("sizes.default %q is not one of the options", r.Sizes.Default)
		}
	}
	if h := r.HeightPx; h != nil {
		if r.Edge != "bottom" {
			bad("height_px applies only to edge bottom")
		}
		lo, def, hi := orDefault(h.Min, 120), orDefault(h.Default, 240), orDefault(h.Max, 640)
		if h.Min < 0 || h.Default < 0 || h.Max < 0 {
			bad("height_px values must be positive")
		} else if lo > def || def > hi {
			bad("height_px must satisfy min <= default <= max (got %d, %d, %d)", lo, def, hi)
		}
	}
	return errs
}

func orDefault(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}
