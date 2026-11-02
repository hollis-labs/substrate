// Package bom reads bom.yaml, the one hand-edited place version pins live, and
// keeps the files that consume those pins in step with it: this module's go.mod
// and CI workflow, and Folio's platform-pins.yaml mirror. `substrate pins sync`
// rewrites them; `substrate pins check` reports the difference.
package bom

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// BOM is the parsed bom.yaml.
type BOM struct {
	Toolchain struct {
		GoFloor string `yaml:"go_floor"` // portfolio floor, mirrored to Folio
		GoMin   string `yaml:"go_min"`   // this module's go directive
	} `yaml:"toolchain"`
	CI         map[string]string `yaml:"ci"`
	Libs       map[string]string `yaml:"libs"`
	ThirdParty map[string]string `yaml:"third_party"`
}

var ciKeys = []string{"actions_checkout", "actions_setup_go", "golangci_lint_action", "golangci_lint_version", "govulncheck_version"}

// Load parses and validates a BOM file.
func Load(path string) (*BOM, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m BOM
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, m.validate()
}

func (b *BOM) validate() error {
	if b.Toolchain.GoFloor == "" || b.Toolchain.GoMin == "" {
		return fmt.Errorf("bom: toolchain.go_floor and toolchain.go_min are required")
	}
	if versionLess(b.Toolchain.GoMin, b.Toolchain.GoFloor) {
		return fmt.Errorf("bom: go_min %s is below go_floor %s", b.Toolchain.GoMin, b.Toolchain.GoFloor)
	}
	for _, k := range ciKeys {
		if b.CI[k] == "" {
			return fmt.Errorf("bom: ci.%s is required", k)
		}
	}
	return nil
}

// Diff is one place a consumer disagrees with the BOM.
type Diff struct{ File, Msg string }

func (d Diff) String() string { return d.File + ": " + d.Msg }

// Targets say which consumers to examine. Empty fields are skipped.
type Targets struct {
	GoMod     string   // path to go.mod
	Workflows []string // paths to CI workflows
	Folio     string   // path to Folio's platform-pins.yaml, or an http(s) URL (check only)
}

// Check returns every disagreement between the BOM and the targets.
func Check(b *BOM, t Targets) ([]Diff, error) {
	var ds []Diff
	if t.GoMod != "" {
		src, err := os.ReadFile(t.GoMod)
		if err != nil {
			return nil, err
		}
		_, d := editGoMod(b, string(src))
		ds = append(ds, tag(t.GoMod, d)...)
	}
	for _, w := range t.Workflows {
		src, err := os.ReadFile(w)
		if err != nil {
			return nil, err
		}
		_, d := editWorkflow(b, string(src))
		ds = append(ds, tag(w, d)...)
	}
	if t.Folio != "" {
		cur, err := readFolio(t.Folio)
		if err != nil {
			return nil, err
		}
		if renderFolio(b, cur) != cur {
			ds = append(ds, Diff{t.Folio, "platform-pins.yaml differs from the BOM (run: substrate pins sync --folio <dir>)"})
		}
	}
	return ds, nil
}

// Sync rewrites the targets from the BOM and returns the files it changed.
// Folio must be a local path.
func Sync(b *BOM, t Targets) ([]string, error) {
	var changed []string
	write := func(path, old, new string) error {
		if old == new {
			return nil
		}
		changed = append(changed, path)
		return os.WriteFile(path, []byte(new), 0o644)
	}
	if t.GoMod != "" {
		src, err := os.ReadFile(t.GoMod)
		if err != nil {
			return nil, err
		}
		out, d := editGoMod(b, string(src))
		if err := write(t.GoMod, string(src), out); err != nil {
			return nil, err
		}
		if rest := unfixable(d); len(rest) > 0 {
			return changed, fmt.Errorf("%s: %s", t.GoMod, strings.Join(rest, "; "))
		}
	}
	for _, w := range t.Workflows {
		src, err := os.ReadFile(w)
		if err != nil {
			return nil, err
		}
		out, _ := editWorkflow(b, string(src))
		if err := write(w, string(src), out); err != nil {
			return nil, err
		}
	}
	if t.Folio != "" {
		if strings.HasPrefix(t.Folio, "http") {
			return changed, fmt.Errorf("cannot sync to a URL")
		}
		cur, err := readFolio(t.Folio)
		if err != nil {
			return nil, err
		}
		if err := write(t.Folio, cur, renderFolio(b, cur)); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// FolioFile resolves a directory or file argument to Folio's platform-pins.yaml.
func FolioFile(arg string) string {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		return arg
	}
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		return filepath.Join(arg, "platform-pins.yaml")
	}
	return arg
}

func tag(file string, msgs []string) []Diff {
	var ds []Diff
	for _, m := range msgs {
		ds = append(ds, Diff{file, m})
	}
	return ds
}

// unfixable filters the messages sync cannot repair by rewriting a line.
func unfixable(msgs []string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, "missing:") || strings.HasPrefix(m, "unpinned:") {
			out = append(out, m)
		}
	}
	return out
}

var (
	goDirective = regexp.MustCompile(`(?m)^go (\S+)$`)
	requireLine = regexp.MustCompile(`(?m)^(\s*(?:require\s+)?)(\S+) (v\S+)(\s*//.*)?$`)
)

// editGoMod returns go.mod with BOM pins applied, and what differed. A pinned
// module go.mod does not require is "missing:", and a direct requirement the
// BOM does not pin is "unpinned:": both need a human, not a rewrite.
func editGoMod(b *BOM, src string) (string, []string) {
	var diffs []string
	out := goDirective.ReplaceAllStringFunc(src, func(m string) string {
		if have := strings.TrimPrefix(m, "go "); have != b.Toolchain.GoMin {
			diffs = append(diffs, fmt.Sprintf("go directive is %s, bom toolchain.go_min is %s", have, b.Toolchain.GoMin))
		}
		return "go " + b.Toolchain.GoMin
	})
	pins := map[string]string{}
	for _, m := range []map[string]string{b.Libs, b.ThirdParty} {
		for k, v := range m {
			pins[k] = v
		}
	}
	seen := map[string]bool{}
	out = requireLine.ReplaceAllStringFunc(out, func(line string) string {
		m := requireLine.FindStringSubmatch(line)
		path, have, indirect := m[2], m[3], strings.Contains(m[4], "indirect")
		if path == "module" || path == "go" || path == "toolchain" {
			return line
		}
		want, pinned := pins[path]
		if indirect {
			return line
		}
		if !pinned {
			diffs = append(diffs, "unpinned: go.mod requires "+path+" directly but the bom does not pin it")
			return line
		}
		seen[path] = true
		if have != want {
			diffs = append(diffs, fmt.Sprintf("%s is %s, bom pins %s", path, have, want))
			return m[1] + path + " " + want + m[4]
		}
		return line
	})
	var names []string
	for p := range pins {
		if !seen[p] {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	for _, p := range names {
		diffs = append(diffs, "missing: bom pins "+p+" but go.mod has no direct requirement")
	}
	return out, diffs
}

// workflowPins maps a BOM key to the text in a workflow that carries it.
var workflowPins = []struct {
	key string
	re  *regexp.Regexp
}{
	{"actions_checkout", regexp.MustCompile(`(actions/checkout@)\S+`)},
	{"actions_setup_go", regexp.MustCompile(`(actions/setup-go@)\S+`)},
	{"actions_upload_artifact", regexp.MustCompile(`(actions/upload-artifact@)\S+`)}, // optional; not mirrored to Folio
	{"golangci_lint_action", regexp.MustCompile(`(golangci/golangci-lint-action@)\S+`)},
	// The lint binary version is the `version:` input on the line after the action.
	{"golangci_lint_version", regexp.MustCompile(`(golangci/golangci-lint-action@\S+\s+with:\s+version: )\S+`)},
}

func editWorkflow(b *BOM, src string) (string, []string) {
	var diffs []string
	out := src
	for _, p := range workflowPins {
		want := b.CI[p.key]
		if want == "" { // optional pin the BOM does not set
			continue
		}
		out = p.re.ReplaceAllStringFunc(out, func(m string) string {
			sub := p.re.FindStringSubmatch(m)
			if sub[0] != sub[1]+want {
				diffs = append(diffs, fmt.Sprintf("ci.%s: workflow has %q, bom pins %q", p.key, strings.TrimPrefix(sub[0], sub[1]), want))
			}
			return sub[1] + want
		})
	}
	return out, diffs
}

func readFolio(loc string) (string, error) {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		c := http.Client{Timeout: 20 * time.Second}
		resp, err := c.Get(loc)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s: %s", loc, resp.Status)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return string(b), err
	}
	b, err := os.ReadFile(loc)
	return string(b), err
}

// renderFolio writes Folio's flat platform-pins.yaml. The block from the
// chimera comment onward is Folio-owned, not a BOM pin, and is carried over.
func renderFolio(b *BOM, current string) string {
	tail := ""
	if i := strings.Index(current, "# Opt-in maintained host recipe"); i >= 0 {
		tail = "\n" + current[i:]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `# platform-pins.yaml — MIRROR of the hollis-labs/substrate bom/bom.yaml
# (toolchain.go_floor and the ci block). Generated by `+"`substrate pins sync --folio`"+`;
# do not edit by hand. The BOM is the source.
#
# Every folio preset's go_version input default, every rendered CI workflow's
# tool-version pins, folio's own dev CI, folio's conformance script floor, and
# LIB-STANDARD-CHECKLIST.md's cited versions must match this file. Checked by
# scripts/check-platform-pins-drift.sh.
#
# This file has no effect on rendering by itself today (folio's preset engine
# does not read it yet) -- see CHANGELOG.md for the wiring follow-up.
#
# Keep it flat: two levels, `+"`key: \"value\"`"+`, one value per line. The drift check
# reads it with awk, not a YAML parser.

go:
  version: %q           # portfolio floor -- decision go_floor_1_26_6_everywhere
  # a per-repo `+"`toolchain`"+` line stays a LOCAL escape hatch (go-baseline's
  # go_toolchain input), never a substitute for this floor on the `+"`go`"+` line.

ci:
  actions_checkout: %q
  actions_setup_go: %q      # v6+ honors a toolchain line; v5 silently ignores it
  golangci_lint_action: %q  # golangci/golangci-lint-action tag
  golangci_lint_version: %q
  govulncheck_version: %q
%s`, b.Toolchain.GoFloor, b.CI["actions_checkout"], b.CI["actions_setup_go"], b.CI["golangci_lint_action"],
		b.CI["golangci_lint_version"], b.CI["govulncheck_version"], tail)
	return sb.String()
}

// versionLess compares dotted numeric versions: a < b.
func versionLess(a, b string) bool {
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
