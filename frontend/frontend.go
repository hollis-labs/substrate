// Package frontend generates the build-time frontend manifest: the merged UI
// contributions of exactly the modules a manifest selects, and the BOM-pinned
// npm packages the frontend needs. Removing a module removes its UI. The
// frontend runtime consumes only this file.
package frontend

import (
	"fmt"
	"sort"
	"strings"

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
	// Items are every selected module's UI contributions, merged and ordered
	// by priority (default 10), then module, then key. Shared nav groups appear once.
	Items  []Item  `json:"items"`
	Routes []Route `json:"routes"`
	// Regions is the host's region catalog from the manifest, so the shell knows
	// which regions are docked and where they are anchored.
	Regions []manifest.Region `json:"regions"`
	Kits    []Kit             `json:"kits"`
}

// DefaultPriority orders contributions that name none.
const DefaultPriority = 10

// Item is a contribution tagged with the module that made it.
type Item struct {
	ui.Item
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
		Contributions: []ui.Contribution{}, Items: []Item{}, Routes: []Route{}, Kits: []Kit{},
		Regions: append([]manifest.Region{}, m.Frontend.Regions...),
	}
	want := map[string]bool{}
	var items []Item
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
		for _, it := range c.Contributions {
			items = append(items, Item{it, name})
		}
		for _, r := range c.Routes {
			out.Routes = append(out.Routes, Route{r, name})
		}
		if c.Package != nil {
			want[c.Package.Name] = true
		}
		if len(c.Contributions) > 0 || c.Package != nil {
			for _, k := range BaseKits {
				want[k] = true
			}
		}
	}
	if out.Items, err = merge(items, out.Routes, m.Frontend.Regions); err != nil {
		return nil, err
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

// merge checks the cross-module rules and returns the items in display order:
// nav items sit in groups that exist, two modules naming a group agree on its
// label (it is listed once), a slot opens a dock.tab that exists, and no
// frontend route equals a backend path or another frontend route.
func merge(in []Item, routes []Route, regions []manifest.Region) ([]Item, error) {
	backend := map[string]string{}
	seen := map[string]string{}
	for _, r := range routes {
		id := r.Method + " " + r.Path
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("route %s is declared by both %s and %s", id, prev, r.Module)
		}
		seen[id] = r.Module
		backend[strings.TrimSuffix(r.Path, "/")] = r.Module
	}

	groups := map[string]Item{}
	front := map[string]string{}
	var out []Item
	for _, it := range in {
		switch it.Kind {
		case "nav.group":
			if g, dup := groups[it.Key]; dup {
				if g.Label != it.Label {
					return nil, fmt.Errorf("nav group %q is labelled %q by %s and %q by %s", it.Key, g.Label, g.Module, it.Label, it.Module)
				}
				continue // already listed
			}
			groups[it.Key] = it
		}
		if it.Route != "" && (it.Kind == "nav.item" || it.Kind == "surface") {
			path := strings.TrimSuffix(it.Route, "/")
			if m, clash := backend[path]; clash {
				return nil, fmt.Errorf("%s %q (%s) uses route %s, which is a backend path served by %s", it.Kind, it.Key, it.Module, it.Route, m)
			}
			if it.Kind == "surface" {
				if prev, dup := front[path]; dup {
					return nil, fmt.Errorf("surface %q (%s) and %s both claim route %s", it.Key, it.Module, prev, it.Route)
				}
				front[path] = it.Module + "/" + it.Key
			}
		}
		out = append(out, it)
	}
	catalog := map[string]manifest.Region{}
	for _, r := range regions {
		catalog[r.Name] = r
	}
	tabRegion := map[string]string{} // dock.tab key -> its region
	for _, it := range out {
		if it.Kind == "dock.tab" {
			tabRegion[it.Key] = it.EffectiveRegion()
		}
	}
	for _, it := range out {
		// dock.tab and slot go in host-defined regions, which the manifest must declare.
		if it.Kind == "dock.tab" || it.Kind == "slot" {
			r, ok := catalog[it.EffectiveRegion()]
			switch {
			case !ok:
				return nil, fmt.Errorf("%s %q (%s) is in region %q, which the manifest does not declare under frontend.regions", it.Kind, it.Key, it.Module, it.EffectiveRegion())
			case it.Kind == "dock.tab" && !r.Docked:
				return nil, fmt.Errorf("dock.tab %q (%s) is in region %q, which is not docked", it.Key, it.Module, r.Name)
			case it.Kind == "slot" && r.Docked:
				return nil, fmt.Errorf("slot %q (%s) is in the docked region %q; slots live in inline regions", it.Key, it.Module, r.Name)
			}
		}
		if it.Kind == "slot" && it.Opens != "" {
			// The tab must sit in the docked region anchored under this slot's region.
			region, ok := tabRegion[it.Opens]
			switch {
			case !ok:
				return nil, fmt.Errorf("slot %q (%s) opens dock.tab %q, which no selected module declares", it.Key, it.Module, it.Opens)
			case catalog[region].Anchor != it.EffectiveRegion():
				return nil, fmt.Errorf("slot %q (%s) in region %q opens dock.tab %q in region %q, which is not anchored under %q", it.Key, it.Module, it.EffectiveRegion(), it.Opens, region, it.EffectiveRegion())
			}
		}
		switch {
		case it.Kind == "nav.item" && strings.HasPrefix(it.EffectiveRegion(), ui.RegionNavGroupPrefix):
			g := strings.TrimPrefix(it.EffectiveRegion(), ui.RegionNavGroupPrefix)
			if _, ok := groups[g]; !ok {
				return nil, fmt.Errorf("nav.item %q (%s) is in group %q, which no selected module declares", it.Key, it.Module, g)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		pa, pb := a.Priority, b.Priority
		if pa == 0 {
			pa = DefaultPriority
		}
		if pb == 0 {
			pb = DefaultPriority
		}
		switch {
		case pa != pb:
			return pa < pb
		case a.Module != b.Module:
			return a.Module < b.Module
		}
		return a.Key < b.Key
	})
	return out, nil
}
