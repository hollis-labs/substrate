package manifest

import (
	"strings"
	"testing"
)

func TestAppConfigBlockAndContributionsPath(t *testing.T) {
	base := "app: myapp\nvariant: service\nprofiles: [minimal]\n"
	ok := map[string]string{
		"config for the app itself": base + "config:\n  myapp: {greeting: hi}\n",
		"config for a module":       base + "config:\n  logging: {level: debug}\n",
		"contributions path":        base + "frontend:\n  contributions: ui/app.json\n",
	}
	for name, src := range ok {
		m, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if errs := m.Validate(); len(errs) > 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
	bad := map[string]struct{ src, want string }{
		"config for another name":   {base + "config:\n  other: {a: 1}\n", "config for unknown module"},
		"app is a catalogue module": {"app: logging\nvariant: service\nprofiles: [minimal]\nfrontend:\n  contributions: a.json\n", "is a catalogue module"},
		"absolute contributions":    {base + "frontend:\n  contributions: /etc/app.json\n", "inside the manifest's directory"},
		"escaping contributions":    {base + "frontend:\n  contributions: ../app.json\n", "inside the manifest's directory"},
	}
	for name, c := range bad {
		m, err := Parse([]byte(c.src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		errs := m.Validate()
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), c.want) {
			t.Errorf("%s: errs = %v, want %q", name, errs, c.want)
		}
	}
}
