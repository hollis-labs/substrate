package manifest

import (
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/catalog"
)

func TestResolveMinimalAppHost(t *testing.T) {
	m, err := Parse([]byte("app: a\nvariant: app-host\nprofiles: [minimal]\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if want := "config,logging,health,httphost"; strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestValidateRejects(t *testing.T) {
	// Every catalogue module is ready today; keep the planned-module rule covered.
	catalog.Modules = append(catalog.Modules, catalog.Module{Name: "not_built_yet", Status: "planned"})
	defer func() { catalog.Modules = catalog.Modules[:len(catalog.Modules)-1] }()
	cases := map[string]string{
		"unknown field":        "app: a\nvariant: service\nprofiles: [minimal]\nbogus: 1\n",
		"unknown variant":      "app: a\nvariant: nope\nprofiles: [minimal]\n",
		"unknown profile":      "app: a\nvariant: service\nprofiles: [nope]\n",
		"unknown module":       "app: a\nvariant: service\nprofiles: [minimal]\nmodules: {add: [nope]}\n",
		"planned module":       "app: a\nvariant: service\nprofiles: [minimal]\nmodules: {add: [not_built_yet]}\n",
		"no app":               "variant: service\nprofiles: [minimal]\n",
		"removed but required": "app: a\nvariant: app-host\nprofiles: [minimal]\nmodules: {remove: [httphost]}\n",
	}
	for name, src := range cases {
		m, err := Parse([]byte(src))
		if err != nil {
			continue // strict parse rejection counts
		}
		if _, err := m.Resolve(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCLIVariantIncludesCLIModule(t *testing.T) {
	m, err := Parse([]byte("app: a\nvariant: cli\nprofiles: [minimal]\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(got, ","), "cli") {
		t.Fatalf("cli variant must start the cli module: %v", got)
	}
}

func TestHostRegionCatalogIsValidated(t *testing.T) {
	base := "app: x\nvariant: app-host\nprofiles: [minimal]\nfrontend:\n  regions:\n"
	for name, c := range map[string]struct{ regions, want string }{
		"fine":                {"    - {name: chat.composer}\n    - {name: chat.drawer, docked: true, anchor: chat.composer}\n", ""},
		"docked needs anchor": {"    - {name: chat.drawer, docked: true}\n", "must declare its anchor"},
		"anchor undeclared":   {"    - {name: chat.drawer, docked: true, anchor: chat.composer}\n", "not declared"},
		"anchor on inline":    {"    - {name: chat.composer, anchor: chat.drawer}\n", "not docked"},
		"anchor is docked":    {"    - {name: a, docked: true, anchor: b}\n    - {name: b, docked: true, anchor: a}\n", "inline region"},
		"core region":         {"    - {name: nav.primary}\n", "core region"},
		"duplicate":           {"    - {name: chat.composer}\n    - {name: chat.composer}\n", "declared twice"},
		"bad name":            {"    - {name: Chat Composer}\n", "valid region name"},
	} {
		m, err := Parse([]byte(base + c.regions))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		errs := m.Validate()
		if c.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: %v", name, errs)
			}
			continue
		}
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e.Error(), c.want)
		}
		if !found {
			t.Errorf("%s: %v, want one containing %q", name, errs, c.want)
		}
	}
}

func TestDockAttributesAreValidated(t *testing.T) {
	base := "app: x\nvariant: app-host\nprofiles: [minimal]\nfrontend:\n  regions:\n    - {name: chat.composer}\n"
	dock := func(attrs string) string {
		return base + "    - {name: chat.drawer, docked: true, anchor: chat.composer, " + attrs + "}\n"
	}
	for name, c := range map[string]struct{ src, want string }{
		"all of them":           {dock(`open_state: open, exclusive: true, sizes: {options: [sm, md, lg], default: md}, dismiss_key: Esc, mobile: bottom-sheet, edge: side`), ""},
		"a bottom dock":         {dock(`edge: bottom, height_px: {min: 100, default: 300, max: 700}`), ""},
		"height defaults":       {dock(`edge: bottom, height_px: {}`), ""},
		"bad open_state":        {dock(`open_state: ajar`), "open_state"},
		"bad size":              {dock(`sizes: {options: [xl], default: xl}`), "sm, md and lg"},
		"no sizes":              {dock(`sizes: {options: [], default: md}`), "at least one size"},
		"default not offered":   {dock(`sizes: {options: [sm], default: lg}`), "not one of the options"},
		"duplicate size":        {dock(`sizes: {options: [sm, sm], default: sm}`), "twice"},
		"bad dismiss key":       {dock(`dismiss_key: Enter`), "dismiss_key"},
		"bad mobile":            {dock(`mobile: fullscreen`), "mobile"},
		"bad edge":              {dock(`edge: top`), "edge"},
		"height on a side dock": {dock(`height_px: {min: 100}`), "only to edge bottom"},
		"height out of order":   {dock(`edge: bottom, height_px: {min: 500, default: 300, max: 700}`), "min <= default <= max"},
		"on an inline region":   {base + "    - {name: chat.message, open_state: open}\n", "docked regions only"},
		"menu region is core":   {base + "    - {name: \"menu:plugin\"}\n", "core region"},
		"palette is core":       {base + "    - {name: command.palette}\n", "core region"},
	} {
		m, err := Parse([]byte(c.src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		errs := m.Validate()
		if c.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: %v", name, errs)
			}
			continue
		}
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e.Error(), c.want)
		}
		if !found {
			t.Errorf("%s: %v, want one containing %q", name, errs, c.want)
		}
	}
}
