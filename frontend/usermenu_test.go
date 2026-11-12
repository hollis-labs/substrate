package frontend

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/ui"
)

// v0.4.1: user.menu is a core region. Two modules contribute; the merged items
// are ordered by order, then module, then key, carry their group, and have
// {app} in requires expanded. The golden is the wire form of the items.
func TestUserMenuGolden(t *testing.T) {
	b := realBOM(t)
	surfaces := `{"kind":"surface","key":"account","route":"/account","layout":"form","title":"Account"},` +
		`{"kind":"surface","key":"keys","route":"/access-keys","layout":"list","title":"Keys"},`
	mi := func(key, group, extra, action string) string {
		return `{"kind":"menu.item","key":"` + key + `","label":"` + key + `","region":"user.menu","group":"` + group + `",` + extra + `"action":` + action + `}`
	}
	fm, err := Build(mf("minimal", "auth", "events"), b, ui.Default, loaders(map[string]string{
		"auth": surfaces +
			mi("signout", "session", `"order":900,`, `{"call":"identity.signOut"}`) + "," +
			mi("keys", "account", `"order":20,"requires":"{app}.admin",`, `{"navigate":"/access-keys"}`) + "," +
			mi("account", "account", `"order":10,`, `{"navigate":"/account"}`),
		"events": mi("billing", "account", `"order":20,`, `{"call":"events.openBilling"}`) + "," +
			mi("signout-all", "session", `"order":900,"risk":"high",`, `{"call":"events.signOutAll"}`),
	}))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, it := range fm.Items {
		if it.EffectiveRegion() != ui.RegionUserMenu {
			continue
		}
		raw, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	golden := strings.Join([]string{
		`{"kind":"menu.item","key":"account","label":"account","region":"user.menu","action":{"navigate":"/account"},"order":10,"group":"account","module":"auth"}`,
		`{"kind":"menu.item","key":"keys","label":"keys","region":"user.menu","requires":"t.admin","action":{"navigate":"/access-keys"},"order":20,"group":"account","module":"auth"}`,
		`{"kind":"menu.item","key":"billing","label":"billing","region":"user.menu","action":{"call":"events.openBilling"},"order":20,"group":"account","module":"events"}`,
		`{"kind":"menu.item","key":"signout","label":"signout","region":"user.menu","action":{"call":"identity.signOut"},"order":900,"group":"session","module":"auth"}`,
		`{"kind":"menu.item","key":"signout-all","label":"signout-all","region":"user.menu","action":{"call":"events.signOutAll"},"risk":"high","order":900,"group":"session","module":"events"}`,
	}, "\n")
	if got := strings.Join(lines, "\n"); got != golden {
		t.Fatalf("user.menu items:\n%s\nwant:\n%s", got, golden)
	}
	// user.menu is implicit: never in the region catalog.
	for _, r := range fm.Regions {
		if r.Name == ui.RegionUserMenu {
			t.Errorf("user.menu was emitted in regions: %+v", fm.Regions)
		}
	}
	// The expanded scope is also in the per-module contributions the shell may read.
	raw, _ := json.Marshal(fm.Contributions)
	if strings.Contains(string(raw), "{app}") {
		t.Errorf("an unexpanded {app} reached the manifest: %s", raw)
	}
}

func TestUserMenuRefusals(t *testing.T) {
	b := realBOM(t)
	surface := `{"kind":"surface","key":"account","route":"/account","layout":"form","title":"Account"}`
	mi := func(extra, action string) string {
		return `{"kind":"menu.item","key":"m","label":"M","region":"user.menu",` + extra + `"action":` + action + `}`
	}
	for name, c := range map[string]struct{ part, want string }{
		"a command":                  {`{"kind":"command","key":"c","label":"C","region":"user.menu","action":{"call":"a.b"}}`, "takes only menu.item"},
		"a nav.item":                 {`{"kind":"nav.item","key":"n","label":"N","region":"user.menu","route":"/x"}`, "takes only menu.item"},
		"a slot":                     {`{"kind":"slot","key":"s","region":"user.menu"}`, "takes only menu.item"},
		"when":                       {mi(`"when":"plugin",`, `{"call":"a.b"}`), "when is not allowed in user.menu"},
		"a call without a module":    {mi(``, `{"call":"signOut"}`), "<module>.<verb>"},
		"a call with a snake verb":   {mi(``, `{"call":"identity.sign_out"}`), "<module>.<verb>"},
		"navigate to no route":       {mi(``, `{"navigate":"/nowhere"}`), "no selected module declares as a surface route"},
		"requires as an expression":  {mi(`"requires":"a && b",`, `{"call":"a.b"}`), "not an expression"},
		"requires with stray braces": {mi(`"requires":"{user}.admin",`, `{"call":"a.b"}`), "not an expression"},
		"a field the kind lacks":     {mi(`"keywords":["k"],`, `{"call":"a.b"}`), `field "keywords" does not apply`},
		"duplicate key":              {mi(``, `{"call":"a.b"}`) + "," + mi(``, `{"call":"a.c"}`), "duplicate menu.item"},
		"unknown risk":               {mi(`"risk":"extreme",`, `{"call":"a.b"}`), "must be low, medium or high"},
	} {
		_, err := Build(mf("minimal", "auth"), b, ui.Default, loaders(map[string]string{"auth": surface + "," + c.part}))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// group and requires are accepted on any menu region, and a plain call name
	// outside user.menu keeps its v0.4 shape.
	ok := `{"kind":"menu.item","key":"g","label":"G","region":"toolbar.primary","group":"x","requires":"*","action":{"call":"openOverlay"}}`
	if _, err := Build(mf("minimal", "auth"), b, ui.Default, loaders(map[string]string{"auth": ok})); err != nil {
		t.Errorf("group and requires on a toolbar menu.item: %v", err)
	}
}

// A module whose ui.json names a companion package the BOM does not pin fails
// the build, naming the module and the package (the identity UI kit relies on this).
func TestUnpinnedPackageNamesModuleAndPackage(t *testing.T) {
	b := realBOM(t)
	load := func(name string) ([]byte, bool, error) {
		return []byte(`{"schema_version":0,"module":"auth","package":{"name":"@hollis-labs/kit-not-in-bom"},
			"contributions":[{"kind":"widget","key":"w","view":"v"}]}`), name == "auth", nil
	}
	_, err := Build(mf("minimal", "auth"), b, ui.Default, load)
	if err == nil || !strings.Contains(err.Error(), "module auth") || !strings.Contains(err.Error(), "@hollis-labs/kit-not-in-bom") || !strings.Contains(err.Error(), "not pinned in the BOM") {
		t.Fatalf("error = %v", err)
	}
}
