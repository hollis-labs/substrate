package frontend

import (
	"encoding/json"
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

// v0.4: the cross-module rules for menu.item and command, ordering, and bindings.
func TestMenuActionsMustLandOnWhatTheManifestDeclares(t *testing.T) {
	b := realBOM(t)
	surface := `{"kind":"surface","key":"plugins","route":"/plugins","layout":"list","title":"Plugins"}`
	tab := `{"kind":"dock.tab","key":"scratch","region":"chat.drawer","view":"v"}`
	item := func(action string) string {
		return `{"kind":"menu.item","key":"m","label":"M","region":"menu:plugin","action":` + action + `}`
	}
	build := func(regions []manifest.Region, parts map[string]string) error {
		_, err := Build(withRegions(mf("minimal", "auth", "events"), regions), b, ui.Default, loaders(parts))
		return err
	}
	for name, ok := range map[string]map[string]string{
		"navigate to a surface route":   {"auth": surface, "events": item(`{"navigate":"/plugins"}`)},
		"open a docked region":          {"auth": item(`{"openRegion":"chat.drawer"}`)},
		"open a tab in its region":      {"auth": tab, "events": item(`{"openRegion":"chat.drawer","tab":"scratch"}`)},
		"call is the gateway's to find": {"auth": item(`{"call":"anything.at.all"}`)},
		"a non-docked host region":      {"auth": `{"kind":"menu.item","key":"h","label":"H","region":"chat.composer","action":{"call":"x"}}`},
	} {
		if err := build(chatRegions, ok); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]struct {
		parts map[string]string
		want  string
	}{
		"navigate to no route":              {map[string]string{"auth": item(`{"navigate":"/nowhere"}`)}, "no selected module declares as a surface route"},
		"navigate to a nav item only":       {map[string]string{"auth": `{"kind":"nav.item","key":"n","label":"N","route":"/only-nav"},` + item(`{"navigate":"/only-nav"}`)}, "no selected module declares as a surface route"},
		"open an undeclared region":         {map[string]string{"auth": item(`{"openRegion":"ghost.drawer"}`)}, "does not declare"},
		"open an inline region":             {map[string]string{"auth": item(`{"openRegion":"chat.composer"}`)}, "not docked"},
		"open a tab in another region":      {map[string]string{"auth": tab, "events": item(`{"openRegion":"other.drawer","tab":"scratch"}`)}, "no selected module declares that tab there"},
		"open a tab nobody declares":        {map[string]string{"auth": item(`{"openRegion":"chat.drawer","tab":"ghost"}`)}, "no selected module declares that tab there"},
		"menu.item in a docked host region": {map[string]string{"auth": `{"kind":"menu.item","key":"h","label":"H","region":"chat.drawer","action":{"call":"x"}}`}, "docked region"},
		"menu.item in an undeclared region": {map[string]string{"auth": `{"kind":"menu.item","key":"h","label":"H","region":"ghost.menu","action":{"call":"x"}}`}, "does not declare"},
	} {
		if err := build(chatRegions, c.parts); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestMenuItemsAreOrderedByOrderThenModuleThenKey(t *testing.T) {
	b := realBOM(t)
	mi := func(key string, order int) string {
		o := ""
		if order != 0 {
			o = `,"order":` + string(rune('0'+order/10)) + string(rune('0'+order%10))
		}
		return `{"kind":"menu.item","key":"` + key + `","label":"L","region":"toolbar.primary","action":{"call":"x"}` + o + `}`
	}
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, loaders(map[string]string{
		"auth":   mi("b", 0) + "," + mi("a", 20),
		"events": mi("c", 20) + "," + mi("d", 99),
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range fm.Items {
		got = append(got, it.Module+"/"+it.Key)
	}
	// order 20 (auth/a, events/c), then 99 (events/d), then the default 100 (auth/b)
	if s := strings.Join(got, ","); s != "auth/a,events/c,events/d,auth/b" {
		t.Fatalf("order = %s", s)
	}
}

func TestBindingsAreTheEmittedEnvelopeKinds(t *testing.T) {
	b := realBOM(t)
	load := func(name string) ([]byte, bool, error) {
		switch name {
		case "auth":
			return []byte(`{"schema_version":0,"module":"auth","envelopes":{"emits":["card@1","list@2"]}}`), true, nil
		case "events":
			return []byte(`{"schema_version":0,"module":"events","envelopes":{"emits":["card@1","card@2"]}}`), true, nil
		}
		return nil, false, nil
	}
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, load)
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Bindings) != 3 || fm.Bindings[0] != (Binding{"card", "1"}) || fm.Bindings[1] != (Binding{"card", "2"}) || fm.Bindings[2] != (Binding{"list", "2"}) {
		t.Fatalf("bindings = %v, want card@1, card@2, list@2 sorted and unique", fm.Bindings)
	}
	none, err := Build(mf("minimal", "auth"), b, ui.Default, func(string) ([]byte, bool, error) { return nil, false, nil })
	if err != nil || none.Bindings == nil {
		t.Fatalf("a manifest with no envelopes still carries an empty bindings list: %v %v", none.Bindings, err)
	}
}

// The shell reads this JSON; its key names are the contract. A Go field rename
// must not change them (the region keys were once the exported Go names).
func TestWireShapeOfRegionsAndNewItems(t *testing.T) {
	b := realBOM(t)
	regions := []manifest.Region{
		{Name: "chat.composer"},
		{Name: "chat.drawer", Docked: true, Anchor: "chat.composer", OpenState: "open", Edge: "side",
			Sizes: &manifest.DockSizes{Options: []string{"sm", "md"}, Default: "md"}, DismissKey: "Esc", Mobile: "bottom-sheet"},
	}
	fm, err := Build(withRegions(mf("minimal", "auth"), regions), b, ui.Default, loaders(map[string]string{
		"auth": `{"kind":"command","key":"c","label":"C","keywords":["k"],"group":"G","risk":"low","action":{"openRegion":"chat.drawer"}}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(fm)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Regions  []map[string]any `json:"regions"`
		Items    []map[string]any `json:"items"`
		Bindings []any            `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if got := wire.Regions[0]; got["name"] != "chat.composer" || got["docked"] != false || got["anchor"] != nil {
		t.Errorf("inline region on the wire: %v", got)
	}
	d := wire.Regions[1]
	sizes, _ := d["sizes"].(map[string]any)
	if d["name"] != "chat.drawer" || d["docked"] != true || d["anchor"] != "chat.composer" || d["open_state"] != "open" ||
		d["edge"] != "side" || d["dismiss_key"] != "Esc" || d["mobile"] != "bottom-sheet" || sizes["default"] != "md" {
		t.Errorf("docked region on the wire: %v", d)
	}
	it := wire.Items[0]
	action, _ := it["action"].(map[string]any)
	if it["kind"] != "command" || it["module"] != "auth" || it["group"] != "G" || it["risk"] != "low" || action["openRegion"] != "chat.drawer" {
		t.Errorf("command on the wire: %v", it)
	}
	if wire.Bindings == nil {
		t.Error("bindings is absent from the wire")
	}
}

// The shell reads items as an array; an app whose modules contribute none must
// still get [] on the wire, not null.
func TestEmptyItemsSerializeAsAnArray(t *testing.T) {
	fm, err := Build(mf("minimal"), realBOM(t), ui.Default, func(string) ([]byte, bool, error) { return nil, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(fm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"items":[]`) {
		t.Fatalf("items is not an empty array: %s", data)
	}
}
