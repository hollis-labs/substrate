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
			"nav":[{"kind":"nav.item","key":"keys","label":"Keys","route":"/keys"}]}`), true, nil
	}
	fm, err := Build(mf("minimal", "auth"), b, ui.Default, load)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range fm.Kits {
		got = append(got, k.Name+"@"+k.Version)
	}
	want := "@hollis-labs/design-app-runtime@" + b.Kits["@hollis-labs/design-app-runtime"]
	if len(got) != 4 || !strings.Contains(strings.Join(got, " "), want) || !strings.Contains(strings.Join(got, " "), "@hollis-labs/kit-admin@"+b.Kits["@hollis-labs/kit-admin"]) {
		t.Fatalf("kits = %v", got)
	}
	b.Kits = map[string]string{}
	if _, err := Build(mf("minimal", "auth"), b, ui.Default, load); err == nil || !strings.Contains(err.Error(), "not pinned in the BOM") {
		t.Fatalf("an unpinned package must be refused: %v", err)
	}
}

func TestNavGroupsMergeAndMustExist(t *testing.T) {
	b := realBOM(t)
	two := func(groupB, labelB, itemGroup string) Load {
		return func(name string) ([]byte, bool, error) {
			switch name {
			case "auth":
				return []byte(`{"schema_version":0,"module":"auth","nav":[
					{"kind":"nav.group","key":"access","label":"Access","order":20},
					{"kind":"nav.item","key":"keys","label":"Keys","group":"` + itemGroup + `","route":"/keys"}]}`), true, nil
			case "events":
				return []byte(`{"schema_version":0,"module":"events","nav":[
					{"kind":"nav.group","key":"` + groupB + `","label":"` + labelB + `","order":20},
					{"kind":"nav.item","key":"stream","label":"Stream","group":"` + groupB + `","route":"/stream"}]}`), true, nil
			}
			return nil, false, nil
		}
	}
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, two("access", "Access", "access"))
	if err != nil {
		t.Fatal(err)
	}
	groups := 0
	for _, n := range fm.Nav {
		if n.Kind == "nav.group" {
			groups++
		}
	}
	if groups != 1 || len(fm.Nav) != 3 {
		t.Fatalf("a shared group should be listed once: %+v", fm.Nav)
	}
	if _, err := Build(mf("minimal", "auth", "events"), b, ui.Default, two("access", "Admin", "access")); err == nil || !strings.Contains(err.Error(), "labelled") {
		t.Errorf("modules disagreeing on a group label: %v", err)
	}
	if _, err := Build(mf("minimal", "auth", "events"), b, ui.Default, two("other", "Other", "missing")); err == nil || !strings.Contains(err.Error(), "no selected module declares") {
		t.Errorf("item naming an absent group: %v", err)
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
