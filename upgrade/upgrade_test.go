package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyAndMayBreak(t *testing.T) {
	v := func(s string) Version {
		x, err := ParseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, c := range []struct {
		from, to string
		kind     Kind
		breaks   bool
	}{
		{"v0.1.0", "v0.1.0", Same, false},
		{"v0.2.0", "v0.1.0", Downgrade, false},
		{"v0.1.0", "v0.1.3", Patch, false},
		{"v0.1.0", "v0.2.0", Minor, true}, // pre-1.0 minor may break
		{"v1.1.0", "v1.2.0", Minor, false},
		{"v1.9.0", "v2.0.0", Major, true},
		{"v0.1.0", "v0.1.1-0.20261104120000-abcdef123456", Unreleased, true},
	} {
		k := Classify(v(c.from), v(c.to))
		if k != c.kind || k.MayBreak(v(c.from)) != c.breaks {
			t.Errorf("%s -> %s: kind %s breaks %v, want %s %v", c.from, c.to, k, k.MayBreak(v(c.from)), c.kind, c.breaks)
		}
	}
	if _, err := ParseVersion("1.2.3"); err == nil {
		t.Error("a version without the leading v must be refused")
	}
}

const changelog = `# Changelog

## [Unreleased]

## [0.3.0] - 2026-12-01

### Breaking

- The ` + "`events`" + ` scope is now ` + "`<app>.stream`" + `. (migration: events-scope)
- Route ` + "`/api/old`" + ` is removed.
  Move callers to ` + "`/api/new`" + `. (migration: none)
- Config key renamed.

### Added

- Something new.

## [0.2.0] - 2026-11-20

### Added

- Fine.

## [0.1.0] - 2026-11-02

First tagged release.
`

func TestParseChangelogFindsBreakingNotes(t *testing.T) {
	rs := ParseChangelog(changelog)
	if len(rs) != 4 || rs[1].Version != "0.3.0" {
		t.Fatalf("releases: %+v", rs)
	}
	n := rs[1].Breaking
	if len(n) != 3 || n[0].Migration != "events-scope" || n[1].Migration != "none" || n[2].Migration != "" {
		t.Fatalf("breaking notes: %+v", n)
	}
	if !strings.Contains(n[1].Text, "Move callers") {
		t.Errorf("continuation line lost: %q", n[1].Text)
	}
	if len(rs[2].Breaking) != 0 {
		t.Errorf("0.2.0 has no breaking section: %+v", rs[2].Breaking)
	}
	from, _ := ParseVersion("v0.1.0")
	to, _ := ParseVersion("v0.3.0")
	got := Between(rs, from, to)
	if len(got) != 2 || got[0].Version != "0.3.0" || got[1].Version != "0.2.0" {
		t.Errorf("Between = %+v", got)
	}
}

// fake is a Runner that answers go list queries from a table of versions and
// records every other command.
type fake struct {
	t        *testing.T
	current  string
	replaced bool
	latest   string
	dirs     map[string]string // version -> module dir holding bom/ and CHANGELOG.md
	ran      []string
	failOn   string // substring of a command to fail
}

func (f *fake) run(dir, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.ran = append(f.ran, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return "boom", errors.New("exit 1")
	}
	switch {
	case strings.Contains(cmd, "{{.Version}}{{if .Replace}}"):
		if f.replaced {
			return f.current + " REPLACED", nil
		}
		return f.current, nil
	case strings.Contains(cmd, "-f {{.Version}} "+Module+"@"):
		q := cmd[strings.LastIndex(cmd, "@")+1:]
		if q == "latest" {
			return f.latest, nil
		}
		return q, nil
	case strings.HasPrefix(cmd, "go mod download -json "+Module+"@"):
		return `{"Dir": "` + f.dirs[cmd[strings.LastIndex(cmd, "@")+1:]] + `"}`, nil
	}
	return "", nil
}

const bomOld = `
toolchain: {go_floor: "1.26.6", go_min: "1.26.10"}
ci: {actions_checkout: v5, actions_setup_go: v6, golangci_lint_action: v7, golangci_lint_version: v2.11.4, govulncheck_version: v1.8.0}
libs: {github.com/hollis-labs/libs/core/store: v0.1.0}
`

func fixtureDir(t *testing.T, bomYAML, cl string) string {
	t.Helper()
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, "bom"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "bom", "bom.yaml"), []byte(bomYAML), 0o644)
	_ = os.WriteFile(filepath.Join(d, "CHANGELOG.md"), []byte(cl), 0o644)
	return d
}

func setup(t *testing.T, current, target string) (*fake, Options, string) {
	t.Helper()
	app := t.TempDir()
	_ = os.WriteFile(filepath.Join(app, "go.mod"), []byte("module example.com/app\n\nrequire "+Module+" "+current+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(app, "go.sum"), []byte("sum-original\n"), 0o644)
	bomNew := strings.Replace(bomOld, "core/store: v0.1.0", "core/store: v0.2.0", 1)
	f := &fake{t: t, current: current, latest: target, dirs: map[string]string{
		current: fixtureDir(t, bomOld, ""),
		target:  fixtureDir(t, bomNew, changelog),
	}}
	return f, Options{Dir: app, Run: f.run, Manifest: "substrate.yaml"}, app
}

func TestMakeReportsPinChangesAndClassifies(t *testing.T) {
	f, o, _ := setup(t, "v0.1.0", "v0.2.0")
	p, err := Make(o)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Minor || len(p.BOM) != 1 || !strings.Contains(p.BOM[0], "core/store: v0.1.0 -> v0.2.0") {
		t.Fatalf("plan: %+v", p)
	}
	_ = f
	var sb strings.Builder
	p.Report(&sb)
	if !strings.Contains(sb.String(), "v0.1.0 -> v0.2.0 (minor)") {
		t.Errorf("report: %s", sb.String())
	}
}

func TestSameDowngradeAndReplaceAreHandled(t *testing.T) {
	_, o, _ := setup(t, "v0.2.0", "v0.2.0")
	if p, err := Make(o); err != nil || p.Kind != Same {
		t.Errorf("same: %+v %v", p, err)
	}
	f, o, _ := setup(t, "v0.2.0", "v0.1.0")
	f.latest = "v0.1.0"
	if _, err := Make(o); err == nil || !strings.Contains(err.Error(), "downgrades") {
		t.Errorf("downgrade: %v", err)
	}
	f, o, _ = setup(t, "v0.1.0", "v0.2.0")
	f.replaced = true
	if _, err := Make(o); err == nil || !strings.Contains(err.Error(), "replace") {
		t.Errorf("replace: %v", err)
	}
}

func TestApplyRunsTheStepsInOrderAndVerifies(t *testing.T) {
	f, o, app := setup(t, "v0.1.0", "v0.1.1")
	f.dirs["v0.1.1"] = fixtureDir(t, bomOld, "# Changelog\n\n## [0.1.1] - 2026-11-05\n\n### Fixed\n\n- A thing.\n")
	_ = os.WriteFile(filepath.Join(app, "substrate.yaml"), []byte("app: x\n"), 0o644)
	p, err := Make(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, o); err != nil {
		t.Fatal(err)
	}
	tail := strings.Join(f.ran[len(f.ran)-4:], " | ")
	for _, want := range []string{"go get " + Module + "@v0.1.1", "go mod tidy", "cmd/substrate@v0.1.1 validate -f substrate.yaml", "go build ./..."} {
		if !strings.Contains(tail, want) {
			t.Errorf("missing step %q in %s", want, tail)
		}
	}
}

func TestFailedVerificationRestoresTheApp(t *testing.T) {
	f, o, app := setup(t, "v0.1.0", "v0.1.1")
	f.dirs["v0.1.1"] = fixtureDir(t, bomOld, "## [0.1.1] - x\n\n### Fixed\n\n- x\n")
	f.failOn = "go build"
	// simulate the go command rewriting the files before the build fails
	orig := f.run
	o.Run = func(dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "get" {
			_ = os.WriteFile(filepath.Join(app, "go.mod"), []byte("rewritten\n"), 0o644)
			_ = os.WriteFile(filepath.Join(app, "go.sum"), []byte("rewritten\n"), 0o644)
		}
		return orig(dir, name, args...)
	}
	p, err := Make(o)
	if err != nil {
		t.Fatal(err)
	}
	err = Apply(p, o)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v", err)
	}
	mod, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	sum, _ := os.ReadFile(filepath.Join(app, "go.sum"))
	if !strings.Contains(string(mod), Module+" v0.1.0") || string(sum) != "sum-original\n" {
		t.Errorf("not restored: %q %q", mod, sum)
	}
}

func TestBreakingChangesNeedAMigrationOrAnExplicitFlag(t *testing.T) {
	f, o, app := setup(t, "v0.1.0", "v0.3.0")
	_ = f
	p, err := Make(o) // changelog: one migrated (not registered here), one "none", one unnamed
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Manual) != 3 {
		t.Fatalf("with no registered migrations all three are manual: %+v", p.Manual)
	}
	if err := Apply(p, o); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("must refuse: %v", err)
	}

	// Register the migration the first entry names: two remain manual.
	ran := false
	o.Migrations = []Migration{{ID: "events-scope", Introduced: "v0.3.0", Description: "rename scope", Apply: func(tx *Tx) error {
		ran = true
		return tx.Write("scope.txt", []byte("t.stream\n"))
	}}}
	p, err = Make(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Manual) != 2 || len(p.Migrations) != 1 {
		t.Fatalf("manual %d migrations %d", len(p.Manual), len(p.Migrations))
	}
	if err := Apply(p, o); err == nil {
		t.Fatal("manual breaking changes must still refuse")
	}
	if ran {
		t.Error("a refused upgrade must not run migrations")
	}

	o.AllowUnmigrated = true
	if err := Apply(p, o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "scope.txt")); string(b) != "t.stream\n" || !ran {
		t.Errorf("migration did not run: %q", b)
	}
}

func TestAMigrationFailureRollsBackItsFiles(t *testing.T) {
	_, o, app := setup(t, "v0.1.0", "v0.3.0")
	o.AllowUnmigrated = true
	o.Migrations = []Migration{{ID: "events-scope", Introduced: "v0.3.0", Apply: func(tx *Tx) error {
		_ = tx.Write("a.txt", []byte("partial"))
		return fmt.Errorf("cannot rewrite")
	}}}
	p, err := Make(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, o); err == nil || !strings.Contains(err.Error(), "events-scope") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "a.txt")); !os.IsNotExist(err) {
		t.Error("a file written by the failed migration survived")
	}
}

func TestMinorUpgradeWithNoChangelogEntriesIsRefused(t *testing.T) {
	f, o, _ := setup(t, "v0.1.0", "v0.2.0")
	f.dirs["v0.2.0"] = fixtureDir(t, bomOld, "# Changelog\n\n## [Unreleased]\n")
	p, err := Make(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, o); err == nil || !strings.Contains(err.Error(), "no entries") {
		t.Fatalf("err = %v", err)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f, o, _ := setup(t, "v0.1.0", "v0.1.1")
	f.dirs["v0.1.1"] = fixtureDir(t, bomOld, "## [0.1.1] - x\n\n### Fixed\n\n- x\n")
	o.DryRun = true
	p, _ := Make(o)
	before := len(f.ran)
	if err := Apply(p, o); err != nil || len(f.ran) != before {
		t.Fatalf("dry run ran commands: %v %v", err, f.ran[before:])
	}
}
