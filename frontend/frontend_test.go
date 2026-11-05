package frontend

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/bom"
	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/ui"
)

func realBOM(t *testing.T) *bom.BOM {
	t.Helper()
	b, err := bom.Load("../bom/bom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// realLoad reads each module's ui.json from this repo.
func realLoad(name string) ([]byte, bool, error) {
	data, err := os.ReadFile(filepath.Join("..", "modules", name, "ui.json"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func mf(profile string, add ...string) *manifest.Manifest {
	return &manifest.Manifest{App: "t", Variant: "app-host", Profiles: []string{profile}, Modules: manifest.Modules{Add: add}}
}

func routes(fm *Manifest) string {
	var out []string
	for _, r := range fm.Routes {
		out = append(out, r.Method+" "+r.Path)
	}
	return strings.Join(out, ",")
}

// Flag test for the frontend: a module that is not selected contributes nothing.
func TestRemovingAModuleRemovesItsContribution(t *testing.T) {
	b := realBOM(t)
	with, err := Build(mf("agentic", "events"), b, ui.Default, realLoad)
	if err != nil {
		t.Fatal(err)
	}
	without, err := Build(mf("agentic"), b, ui.Default, realLoad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(routes(with), "GET /api/events") {
		t.Fatalf("events selected but its route is missing: %s", routes(with))
	}
	if strings.Contains(routes(without), "/api/events") {
		t.Fatalf("events not selected but its route is present: %s", routes(without))
	}
	for _, want := range []string{"GET /api/capabilities", "GET /api/me"} {
		if !strings.Contains(routes(without), want) {
			t.Errorf("agentic should carry %s: %s", want, routes(without))
		}
	}
}

func TestKitsComeFromTheBOMAndOnlyWhenThereIsUI(t *testing.T) {
	b := realBOM(t)
	routesOnly, err := Build(mf("agentic"), b, ui.Default, realLoad)
	if err != nil || len(routesOnly.Kits) != 0 {
		t.Fatalf("routes-only contributions need no kits: %v %+v", err, routesOnly.Kits)
	}
	load := func(name string) ([]byte, bool, error) {
		if name != "auth" {
			return nil, false, nil
		}
		return []byte(`{"schema_version":0,"module":"auth","package":{"name":"@hollis-labs/kit-admin"},
			"contributions":[{"kind":"nav.item","key":"keys","label":"Keys","route":"/keys"}]}`), true, nil
	}
	fm, err := Build(mf("minimal", "auth"), b, ui.Default, load)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range fm.Kits {
		got = append(got, k.Name+"@"+k.Version)
	}
	all := strings.Join(got, " ")
	if len(got) != 4 || !strings.Contains(all, "@hollis-labs/design-app-runtime@"+b.Kits["@hollis-labs/design-app-runtime"]) ||
		!strings.Contains(all, "@hollis-labs/kit-admin@"+b.Kits["@hollis-labs/kit-admin"]) {
		t.Fatalf("kits = %v", got)
	}
	b.Kits = map[string]string{}
	if _, err := Build(mf("minimal", "auth"), b, ui.Default, load); err == nil || !strings.Contains(err.Error(), "not pinned in the BOM") {
		t.Fatalf("an unpinned package must be refused: %v", err)
	}
}

// loaders builds a Load from per-module contribution lists.
func loaders(parts map[string]string) Load {
	return func(name string) ([]byte, bool, error) {
		items, ok := parts[name]
		if !ok {
			return nil, false, nil
		}
		return []byte(`{"schema_version":0,"module":"` + name + `","contributions":[` + items + `]}`), true, nil
	}
}

func TestSharedNavGroupsAreListedOnceAndMustExist(t *testing.T) {
	b := realBOM(t)
	group := func(label string) string { return `{"kind":"nav.group","key":"access","label":"` + label + `"}` }
	item := func(key, region string) string {
		return `{"kind":"nav.item","key":"` + key + `","label":"X","region":"` + region + `","route":"/` + key + `"}`
	}
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, loaders(map[string]string{
		"auth":   group("Access") + "," + item("keys", "nav.group:access"),
		"events": group("Access") + "," + item("stream", "nav.group:access"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	groups := 0
	for _, it := range fm.Items {
		if it.Kind == "nav.group" {
			groups++
		}
	}
	if groups != 1 || len(fm.Items) != 3 {
		t.Fatalf("a shared group should be listed once: %+v", fm.Items)
	}
	if _, err := Build(mf("minimal", "auth", "events"), b, ui.Default, loaders(map[string]string{
		"auth": group("Access"), "events": group("Admin"),
	})); err == nil || !strings.Contains(err.Error(), "labelled") {
		t.Errorf("modules disagreeing on a group label: %v", err)
	}
	if _, err := Build(mf("minimal", "auth"), b, ui.Default, loaders(map[string]string{
		"auth": item("keys", "nav.group:missing"),
	})); err == nil || !strings.Contains(err.Error(), "no selected module declares") {
		t.Errorf("item in an absent group: %v", err)
	}
}

func TestFrontendRoutesNeverEqualBackendPaths(t *testing.T) {
	b := realBOM(t)
	// The events module serves GET /api/events; a frontend nav route may not be that path,
	// and a /admin/ backend path may not be claimed by a surface.
	_, err := Build(mf("agentic", "events"), b, ui.Default, func(name string) ([]byte, bool, error) {
		if name == "events" {
			return []byte(`{"schema_version":0,"module":"events",
				"routes":[{"method":"GET","path":"/admin/manifest","scope":"authenticated"}],
				"contributions":[{"kind":"surface","key":"m","route":"/admin/manifest","layout":"list","title":"M"}]}`), true, nil
		}
		return nil, false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "backend path") {
		t.Fatalf("a surface on a backend path must be refused: %v", err)
	}
}

var chatRegions = []manifest.Region{
	{Name: "chat.composer"},
	{Name: "chat.message"},
	{Name: "chat.drawer", Docked: true, Anchor: "chat.composer"},
	{Name: "other.drawer", Docked: true, Anchor: "chat.message"},
}

func withRegions(m *manifest.Manifest, rs []manifest.Region) *manifest.Manifest {
	m.Frontend.Regions = rs
	return m
}

func TestSlotsOpenTabsInTheDockedRegionAnchoredUnderThem(t *testing.T) {
	b := realBOM(t)
	tab := `{"kind":"dock.tab","key":"scratch","region":"chat.drawer","view":"v"}`
	slot := func(region, opens string) string {
		return `{"kind":"slot","key":"t","region":"` + region + `","opens":"` + opens + `"}`
	}
	build := func(regions []manifest.Region, parts map[string]string) error {
		_, err := Build(withRegions(mf("minimal", "auth", "events"), regions), b, ui.Default, loaders(parts))
		return err
	}

	if err := build(chatRegions, map[string]string{"auth": slot("chat.composer", "scratch"), "events": tab}); err != nil {
		t.Errorf("a slot opening a tab in the drawer anchored under it: %v", err)
	}
	for name, c := range map[string]struct {
		regions []manifest.Region
		parts   map[string]string
		want    string
	}{
		"opens nothing":                 {chatRegions, map[string]string{"auth": slot("chat.composer", "ghost")}, "no selected module declares"},
		"opens a tab in another drawer": {chatRegions, map[string]string{"auth": slot("chat.message", "scratch"), "events": tab}, "not anchored under"},
		"undeclared region":             {nil, map[string]string{"auth": slot("chat.composer", "scratch"), "events": tab}, "does not declare"},
		"tab in an inline region":       {chatRegions, map[string]string{"auth": `{"kind":"dock.tab","key":"x","region":"chat.composer","view":"v"}`}, "not docked"},
		"slot in a docked region":       {chatRegions, map[string]string{"auth": `{"kind":"slot","key":"x","region":"chat.drawer"}`}, "docked region"},
	} {
		err := build(c.regions, c.parts)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestItemsAreOrderedByPriorityThenModuleThenKey(t *testing.T) {
	b := realBOM(t)
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, loaders(map[string]string{
		"auth":   `{"kind":"widget","key":"b","view":"v"},{"kind":"widget","key":"a","view":"v","priority":5}`,
		"events": `{"kind":"widget","key":"c","view":"v","priority":5}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, it := range fm.Items {
		order = append(order, it.Module+"/"+it.Key)
	}
	if got := strings.Join(order, ","); got != "auth/a,events/c,auth/b" {
		t.Fatalf("order = %s", got)
	}
}

func TestBadContributionsAreRefused(t *testing.T) {
	b := realBOM(t)
	for name, data := range map[string]string{
		"wrong module":   `{"schema_version":0,"module":"other"}`,
		"unknown field":  `{"schema_version":0,"module":"auth","surprise":1}`,
		"unscoped route": `{"schema_version":0,"module":"auth","routes":[{"method":"GET","path":"/api/x","scope":""}]}`,
	} {
		load := func(n string) ([]byte, bool, error) { return []byte(data), n == "auth", nil }
		if _, err := Build(mf("minimal", "auth"), b, ui.Default, load); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	boom := errors.New("boom")
	if _, err := Build(mf("minimal"), b, ui.Default, func(string) ([]byte, bool, error) { return nil, false, boom }); !errors.Is(err, boom) {
		t.Errorf("loader error lost: %v", err)
	}
}
