// Package frontend generates the build-time frontend manifest: the merged UI
// contributions of exactly the modules a manifest selects, and the BOM-pinned
// npm packages the frontend needs. Removing a module removes its UI. The
// frontend runtime consumes only this file.
package frontend

import (
	"fmt"
	"sort"

	"github.com/hollis-labs/substrate/bom"
	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/ui"
)

// ManifestVersion is the generated manifest's schema version. It is under
// Substrate semver, like the contribution manifest it merges.
const ManifestVersion = 0

// BaseKits are needed by any binary that contributes UI beyond routes.
var BaseKits = []string{
	"@hollis-labs/design-tokens",
	"@hollis-labs/design-components",
	"@hollis-labs/design-app-runtime",
}

// Manifest is the generated artifact.
type Manifest struct {
	ManifestVersion int               `json:"manifest_version"`
	App             string            `json:"app"`
	Variant         string            `json:"variant"`
	Profiles        []string          `json:"profiles"`
	Modules         []string          `json:"modules"`
	Contributions   []ui.Contribution `json:"contributions"`
	Nav             []Nav             `json:"nav"`
	Settings        []Settings        `json:"settings"`
	Routes          []Route           `json:"routes"`
	Kits            []Kit             `json:"kits"`
}

// Nav is a merged navigation entry, tagged with the module that contributed it.
type Nav struct {
	ui.NavEntry
	Module string `json:"module"`
}

// Settings is a settings group, tagged with its module.
type Settings struct {
	ui.Settings
	Module string `json:"module"`
}

// Route is a declared endpoint, tagged with its module.
type Route struct {
	ui.Route
	Module string `json:"module"`
}

// Kit is a pinned npm package.
type Kit struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Load returns a module's ui.json, and false when the module has none.
type Load func(module string) (data []byte, ok bool, err error)

// Build merges the contributions of m's resolved modules.
func Build(m *manifest.Manifest, b *bom.BOM, v ui.Vocabulary, load Load) (*Manifest, error) {
	mods, err := m.Resolve()
	if err != nil {
		return nil, err
	}
	out := &Manifest{
		ManifestVersion: ManifestVersion, App: m.App, Variant: m.Variant, Profiles: m.Profiles, Modules: mods,
		Contributions: []ui.Contribution{}, Nav: []Nav{}, Settings: []Settings{}, Routes: []Route{}, Kits: []Kit{},
	}
	want := map[string]bool{}
	hasUI := false
	for _, name := range mods {
		data, ok, err := load(name)
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", name, err)
		}
		if !ok {
			continue
		}
		c, err := ui.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", name, err)
		}
		if c.Module != name {
			return nil, fmt.Errorf("module %s: ui.json says module %q", name, c.Module)
		}
		if errs := ui.Validate(c, v); len(errs) > 0 {
			return nil, errs[0]
		}
		out.Contributions = append(out.Contributions, c)
		for _, n := range c.Nav {
			out.Nav = append(out.Nav, Nav{n, name})
		}
		for _, s := range c.Settings {
			out.Settings = append(out.Settings, Settings{s, name})
		}
		for _, r := range c.Routes {
			out.Routes = append(out.Routes, Route{r, name})
		}
		if c.Package != nil {
			want[c.Package.Name] = true
		}
		if len(c.Nav)+len(c.Settings)+len(c.Panels)+len(c.Widgets) > 0 || c.Package != nil {
			hasUI = true
		}
	}
	if out.Nav, err = mergeNav(out.Nav); err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, r := range out.Routes {
		id := r.Method + " " + r.Path
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("route %s is declared by both %s and %s", id, prev, r.Module)
		}
		seen[id] = r.Module
	}
	if hasUI {
		for _, k := range BaseKits {
			want[k] = true
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ver, ok := b.Kits[n]
		if !ok {
			return nil, fmt.Errorf("package %s is not pinned in the BOM kits section", n)
		}
		out.Kits = append(out.Kits, Kit{n, ver})
	}
	return out, nil
}

// mergeNav checks that every item's group exists and that two modules naming
// the same group agree on its label (it is then listed once), and orders the
// result by order, then module and key, so output is stable.
func mergeNav(in []Nav) ([]Nav, error) {
	groups := map[string]Nav{}
	var out []Nav
	for _, n := range in {
		if n.Kind == "nav.group" {
			if g, dup := groups[n.Key]; dup {
				if g.Label != n.Label {
					return nil, fmt.Errorf("nav group %q is labelled %q by %s and %q by %s", n.Key, g.Label, g.Module, n.Label, n.Module)
				}
				if n.Order != 0 && g.Order == 0 {
					groups[n.Key] = n
				}
				continue
			}
			groups[n.Key] = n
		}
	}
	for _, n := range in {
		switch {
		case n.Kind == "nav.group":
			continue
		case n.Group != "":
			if _, ok := groups[n.Group]; !ok {
				return nil, fmt.Errorf("nav item %q (%s) names group %q, which no selected module declares", n.Key, n.Module, n.Group)
			}
		}
		out = append(out, n)
	}
	for _, g := range groups {
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		return a.Key < b.Key
	})
	if out == nil {
		out = []Nav{}
	}
	return out, nil
}
