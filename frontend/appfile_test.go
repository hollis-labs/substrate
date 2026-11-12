package frontend

import (
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/ui"
)

// appManifest is a manifest whose app names its own contribution file.
func appManifest(add ...string) *manifest.Manifest {
	m := mf("agentic", add...)
	m.App = "myapp"
	m.Frontend.Contributions = "ui/app.json"
	return m
}

func appFile(body string) Option {
	return WithAppFile(func(path string) ([]byte, error) {
		if path != "ui/app.json" {
			return nil, os.ErrNotExist
		}
		return []byte(body), nil
	})
}

func appJSON(module, extra string) string {
	return `{"schema_version":0,"module":"` + module + `"` + extra + `}`
}

func TestAppFileContributesItemsKitsAndBindings(t *testing.T) {
	b := realBOM(t)
	fm, err := Build(appManifest(), b, ui.Default, realLoad, appFile(appJSON("myapp", `,
		"package":{"name":"@hollis-labs/kit-admin"},
		"envelopes":{"emits":["note@1"]},
		"contributions":[
			{"kind":"nav.group","key":"work","label":"Work"},
			{"kind":"nav.item","key":"tasks","label":"Tasks","region":"nav.group:work","route":"/tasks"},
			{"kind":"surface","key":"tasks","route":"/tasks","layout":"list","title":"Tasks"}]`)))
	if err != nil {
		t.Fatal(err)
	}
	var tagged int
	for _, it := range fm.Items {
		if it.Module == "myapp" {
			tagged++
		}
	}
	if tagged != 3 {
		t.Fatalf("want 3 items tagged myapp: %+v", fm.Items)
	}
	if len(fm.Bindings) != 1 || fm.Bindings[0] != (Binding{"note", "1"}) {
		t.Errorf("bindings = %+v", fm.Bindings)
	}
	var kits []string
	for _, k := range fm.Kits {
		kits = append(kits, k.Name)
	}
	if got := strings.Join(kits, " "); !strings.Contains(got, "design-app-runtime") || !strings.Contains(got, "kit-admin") {
		t.Errorf("app file should pull in the base kits and its package: %v", kits)
	}
	if len(fm.Contributions) == 0 || fm.Contributions[len(fm.Contributions)-1].Module != "myapp" {
		t.Errorf("the app's contribution is recorded last: %+v", fm.Contributions)
	}
}

func TestAppFileIsOptional(t *testing.T) {
	m := appManifest()
	m.Frontend.Contributions = ""
	if _, err := Build(m, realBOM(t), ui.Default, realLoad); err != nil {
		t.Fatal(err)
	}
}

func TestAppFileRefusals(t *testing.T) {
	b := realBOM(t)
	nav := `"contributions":[{"kind":"nav.item","key":"x","label":"X","route":"/x"}]`
	for name, c := range map[string]struct {
		opt  Option
		want string
	}{
		"missing file names the path": {WithAppFile(func(string) ([]byte, error) { return nil, os.ErrNotExist }), "ui/app.json"},
		"module is not the app":       {appFile(appJSON("events", `,`+nav)), `want the app name "myapp"`},
		"routes are refused":          {appFile(appJSON("myapp", `,"routes":[{"method":"GET","path":"/api/x","scope":"authenticated"}]`)), "routes are not accepted"},
		"vocabulary is enforced":      {appFile(appJSON("myapp", `,"contributions":[{"kind":"bogus","key":"x"}]`)), "bogus"},
		"bad json":                    {appFile(`{`), "ui/app.json"},
		"unpinned package":            {appFile(appJSON("myapp", `,"package":{"name":"@hollis-labs/not-pinned"}`)), "not pinned in the BOM"},
	} {
		if _, err := Build(appManifest(), b, ui.Default, realLoad, c.opt); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := Build(appManifest(), b, ui.Default, realLoad); err == nil || !strings.Contains(err.Error(), "ui/app.json") {
		t.Errorf("a named file with no reader must fail naming the path: %v", err)
	}
}

// The app's items go through the same merge as a module's.
func TestAppItemsFollowTheCrossContributionRules(t *testing.T) {
	b := realBOM(t)
	modLoad := func(items string) Load {
		return func(name string) ([]byte, bool, error) {
			if name == "events" && items != "" {
				return []byte(`{"schema_version":0,"module":"events","contributions":[` + items + `]}`), true, nil
			}
			return nil, false, nil
		}
	}
	group := func(label string) string { return `{"kind":"nav.group","key":"access","label":"` + label + `"}` }
	for name, c := range map[string]struct {
		mod, app string
		regions  []manifest.Region
		want     string // "" means must succeed
	}{
		"shares a module's group":        {group("Access"), `"contributions":[` + group("Access") + `,{"kind":"nav.item","key":"k","label":"K","region":"nav.group:access","route":"/k"}]`, nil, ""},
		"group label disagrees":          {group("Access"), `"contributions":[` + group("Admin") + `]`, nil, "labelled"},
		"item in an absent group":        {"", `"contributions":[{"kind":"nav.item","key":"k","label":"K","region":"nav.group:nope","route":"/k"}]`, nil, "no selected module declares"},
		"two surfaces claim one route":   {`{"kind":"surface","key":"s","route":"/s","layout":"list","title":"S"}`, `"contributions":[{"kind":"surface","key":"t","route":"/s","layout":"list","title":"T"}]`, nil, "both claim route"},
		"undeclared region":              {"", `"contributions":[{"kind":"dock.tab","key":"d","region":"chat.drawer","view":"v"}]`, nil, "does not declare"},
		"declared region":                {"", `"contributions":[{"kind":"dock.tab","key":"d","region":"chat.drawer","view":"v"}]`, chatRegions, ""},
		"action navigates to a surface":  {"", `"contributions":[{"kind":"surface","key":"s","route":"/s","layout":"list","title":"S"},{"kind":"command","key":"c","label":"C","action":{"navigate":"/s"}}]`, nil, ""},
		"action navigates to nowhere":    {"", `"contributions":[{"kind":"command","key":"c","label":"C","action":{"navigate":"/nowhere"}}]`, nil, "no selected module declares as a surface route"},
		"action opens an undeclared one": {"", `"contributions":[{"kind":"command","key":"c","label":"C","action":{"openRegion":"chat.drawer"}}]`, nil, "does not declare"},
		"slot opens a module's tab":      {`{"kind":"dock.tab","key":"scratch","region":"chat.drawer","view":"v"}`, `"contributions":[{"kind":"slot","key":"t","region":"chat.composer","opens":"scratch"}]`, chatRegions, ""},
	} {
		m := withRegions(appManifest("events"), c.regions)
		_, err := Build(m, b, ui.Default, modLoad(c.mod), appFile(appJSON("myapp", ","+c.app)))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// A module's backend route still clashes with an app surface.
func TestAppSurfaceCannotShadowAModuleRoute(t *testing.T) {
	load := func(name string) ([]byte, bool, error) {
		if name != "events" {
			return nil, false, nil
		}
		return []byte(`{"schema_version":0,"module":"events","routes":[{"method":"GET","path":"/admin/manifest","scope":"authenticated"}]}`), true, nil
	}
	_, err := Build(appManifest("events"), realBOM(t), ui.Default, load, appFile(appJSON("myapp",
		`,"contributions":[{"kind":"surface","key":"s","route":"/admin/manifest","layout":"list","title":"S"}]`)))
	if err == nil || !strings.Contains(err.Error(), "backend path served by events") {
		t.Fatalf("err = %v", err)
	}
}
