package manifest

import (
	"strings"
	"testing"
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
	cases := map[string]string{
		"unknown field":        "app: a\nvariant: service\nprofiles: [minimal]\nbogus: 1\n",
		"unknown variant":      "app: a\nvariant: nope\nprofiles: [minimal]\n",
		"unknown profile":      "app: a\nvariant: service\nprofiles: [nope]\n",
		"unknown module":       "app: a\nvariant: service\nprofiles: [minimal]\nmodules: {add: [nope]}\n",
		"planned module":       "app: a\nvariant: service\nprofiles: [minimal]\nmodules: {add: [mcp]}\n",
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
