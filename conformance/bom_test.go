package conformance

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type bom struct {
	Toolchain struct {
		GoFloor string `yaml:"go_floor"`
		GoMin   string `yaml:"go_min"`
	} `yaml:"toolchain"`
	CI         map[string]string `yaml:"ci"`
	Libs       map[string]string `yaml:"libs"`
	ThirdParty map[string]string `yaml:"third_party"`
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loadBOM(t *testing.T) bom {
	t.Helper()
	var b bom
	if err := yaml.Unmarshal([]byte(read(t, "../bom/bom.yaml")), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// The BOM is the one place pins live; go.mod and CI must say the same thing, so
// bumping a pin means changing the BOM and the file that consumes it together.
func TestBOMMatchesGoMod(t *testing.T) {
	b := loadBOM(t)
	mod := read(t, "../go.mod")

	if m := regexp.MustCompile(`(?m)^go (\S+)$`).FindStringSubmatch(mod); m == nil || m[1] != b.Toolchain.GoMin {
		t.Errorf("go.mod go directive %v != bom toolchain.go_min %q", m, b.Toolchain.GoMin)
	}
	if lessVersion(b.Toolchain.GoMin, b.Toolchain.GoFloor) {
		t.Errorf("go_min %s is below go_floor %s", b.Toolchain.GoMin, b.Toolchain.GoFloor)
	}

	req := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(\S+) (v\S+)$`).FindAllStringSubmatch(mod, -1) {
		if !strings.Contains(m[0], "// indirect") {
			req[m[1]] = m[2]
		}
	}
	for _, pins := range []map[string]string{b.Libs, b.ThirdParty} {
		for path, want := range pins {
			if got := req[path]; got != want {
				t.Errorf("%s: go.mod has %q, bom pins %q", path, got, want)
			}
		}
	}
	// Every direct hollis-labs/libs or third-party requirement must be pinned.
	for path := range req {
		if path == "gopkg.in/yaml.v3" { // manifest parsing; not a framework pin
			continue
		}
		if _, ok := b.Libs[path]; !ok {
			if _, ok := b.ThirdParty[path]; !ok {
				t.Errorf("go.mod requires %s directly but the bom does not pin it", path)
			}
		}
	}
}

func TestBOMMatchesCI(t *testing.T) {
	b := loadBOM(t)
	ci := read(t, "../.github/workflows/ci.yml")
	for _, c := range []struct{ key, needle string }{
		{"actions_checkout", "actions/checkout@"},
		{"actions_setup_go", "actions/setup-go@"},
		{"golangci_lint_action", "golangci/golangci-lint-action@"},
	} {
		if !strings.Contains(ci, c.needle+b.CI[c.key]) {
			t.Errorf("ci.yml does not use %s%s (bom ci.%s)", c.needle, b.CI[c.key], c.key)
		}
	}
	if want := "version: " + b.CI["golangci_lint_version"]; !strings.Contains(ci, want) {
		t.Errorf("ci.yml does not contain %q (bom ci.golangci_lint_version)", want)
	}
}

// lessVersion compares dotted numeric versions: a < b.
func lessVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			if len(pa[i]) != len(pb[i]) {
				return len(pa[i]) < len(pb[i])
			}
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}
