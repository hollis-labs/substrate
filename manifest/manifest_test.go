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
