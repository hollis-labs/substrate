package upgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/substrate/bom"
)

// Module is the Substrate module path an app requires.
const Module = "github.com/hollis-labs/substrate"

// Runner runs a command in dir and returns its combined output.
type Runner func(dir, name string, args ...string) (string, error)

// Options configure Make and Apply.
type Options struct {
	Dir             string // the app's directory (holds go.mod)
	To              string // target version or query; "" means latest
	Manifest        string // path to substrate.yaml, relative to Dir; "" skips validation
	DryRun          bool
	AllowUnmigrated bool // proceed although a breaking change has no mechanical migration
	Run             Runner
	Migrations      []Migration // defaults to the registered ones
}

// Plan is what an upgrade would do.
type Plan struct {
	From, To   Version
	Kind       Kind
	BOM        []string // pin changes, one per line
	Releases   []Release
	Migrations []Migration    // applied, oldest first
	Manual     []BreakingNote // breaking changes with no mechanical migration
}

// Make works out the plan without changing anything.
func Make(o Options) (*Plan, error) {
	if o.Migrations == nil {
		o.Migrations = Migrations
	}
	cur, err := o.Run(o.Dir, "go", "list", "-m", "-f", "{{.Version}}{{if .Replace}} REPLACED{{end}}", Module)
	cur = strings.TrimSpace(cur)
	if err != nil {
		return nil, fmt.Errorf("this app does not require %s: %w", Module, err)
	}
	if strings.HasSuffix(cur, "REPLACED") {
		return nil, fmt.Errorf("the app replaces %s with a local path; remove the replace directive first, an upgrade cannot reason about unreleased local code", Module)
	}
	from, err := ParseVersion(cur)
	if err != nil {
		return nil, err
	}
	query := "latest"
	if o.To != "" {
		query = o.To
	}
	out, err := o.Run(o.Dir, "go", "list", "-m", "-mod=mod", "-f", "{{.Version}}", Module+"@"+query)
	if err != nil {
		return nil, fmt.Errorf("resolve %s@%s: %w\n%s", Module, query, err, out)
	}
	to, err := ParseVersion(strings.TrimSpace(out))
	if err != nil {
		return nil, err
	}
	p := &Plan{From: from, To: to, Kind: Classify(from, to)}
	switch p.Kind {
	case Same:
		return p, nil
	case Downgrade:
		return nil, fmt.Errorf("%s is older than the %s the app uses; downgrades are not supported", to.Raw, from.Raw)
	}

	oldDir, err := moduleDir(o, from.Raw)
	if err != nil {
		return nil, err
	}
	newDir, err := moduleDir(o, to.Raw)
	if err != nil {
		return nil, err
	}
	oldBOM, err := bom.Load(filepath.Join(oldDir, "bom", "bom.yaml"))
	if err != nil {
		return nil, err
	}
	newBOM, err := bom.Load(filepath.Join(newDir, "bom", "bom.yaml"))
	if err != nil {
		return nil, err
	}
	p.BOM = DiffBOM(oldBOM, newBOM)

	if cl, err := os.ReadFile(filepath.Join(newDir, "CHANGELOG.md")); err == nil {
		p.Releases = Between(ParseChangelog(string(cl)), from, to)
	}
	if p.Migrations, err = applicable(o.Migrations, from, to); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, m := range o.Migrations {
		have[m.ID] = true
	}
	for _, r := range p.Releases {
		for _, n := range r.Breaking {
			if n.Migration == "" || n.Migration == "none" || !have[n.Migration] {
				p.Manual = append(p.Manual, n)
			}
		}
	}
	return p, nil
}

func moduleDir(o Options, version string) (string, error) {
	// go mod download populates the module cache and reports where; go list -m
	// leaves Dir empty for a version it has only resolved.
	out, err := o.Run(o.Dir, "go", "mod", "download", "-json", Module+"@"+version)
	if err != nil {
		return "", fmt.Errorf("fetch %s@%s: %w\n%s", Module, version, err, out)
	}
	var d struct{ Dir string }
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.Dir == "" {
		return "", fmt.Errorf("fetch %s@%s: no module directory in %q", Module, version, strings.TrimSpace(out))
	}
	return d.Dir, nil
}

// Refusal returns why the upgrade must not proceed unattended, or "".
func (p *Plan) Refusal(o Options) string {
	if p.Kind == Same {
		return ""
	}
	var why []string
	for _, n := range p.Manual {
		why = append(why, "breaking change without a mechanical migration: "+n.Text)
	}
	if p.Kind.MayBreak(p.From) && len(p.Releases) == 0 {
		why = append(why, fmt.Sprintf("%s -> %s may break an app, and the changelog has no entries for the range to check", p.From.Raw, p.To.Raw))
	}
	if len(why) == 0 || o.AllowUnmigrated {
		return ""
	}
	return strings.Join(why, "\n") + "\nread the changelog, make the changes by hand, and rerun with -allow-unmigrated"
}

// Report writes the plan for a human.
func (p *Plan) Report(w io.Writer) {
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	if p.Kind == Same {
		say("already on %s", p.To.Raw)
		return
	}
	say("%s -> %s (%s)", p.From.Raw, p.To.Raw, p.Kind)
	if len(p.BOM) == 0 {
		say("pins: no change")
	} else {
		say("pins:")
		for _, l := range p.BOM {
			say("  %s", l)
		}
	}
	for _, r := range p.Releases {
		say("changelog [%s]: %d breaking", r.Version, len(r.Breaking))
	}
	for _, m := range p.Migrations {
		say("migration %s: %s", m.ID, m.Description)
	}
	for _, n := range p.Manual {
		say("MANUAL: %s", n.Text)
	}
}

// Apply performs the upgrade and verifies it, restoring the app on any failure.
func Apply(p *Plan, o Options) (err error) {
	if p.Kind == Same {
		return nil
	}
	if why := p.Refusal(o); why != "" {
		return fmt.Errorf("refusing to upgrade:\n%s", why)
	}
	if o.DryRun {
		return nil
	}
	tx := NewTx(o.Dir)
	defer func() {
		if err != nil {
			if rb := tx.Rollback(); rb != nil {
				err = fmt.Errorf("%w (and restoring the app failed: %v)", err, rb)
			} else {
				err = fmt.Errorf("%w\nthe app was restored to %s", err, p.From.Raw)
			}
		}
	}()
	for _, f := range []string{"go.mod", "go.sum"} {
		if err = tx.Snapshot(f); err != nil {
			return err
		}
	}
	for _, m := range p.Migrations {
		if err = m.Apply(tx); err != nil {
			return fmt.Errorf("migration %s: %w", m.ID, err)
		}
	}
	steps := [][]string{
		{"go", "get", Module + "@" + p.To.Raw},
		{"go", "mod", "tidy"},
	}
	if o.Manifest != "" {
		steps = append(steps, []string{"go", "run", Module + "/cmd/substrate@" + p.To.Raw, "validate", "-f", o.Manifest})
	}
	steps = append(steps, []string{"go", "build", "./..."})
	for _, s := range steps {
		if out, e := o.Run(o.Dir, s[0], s[1:]...); e != nil {
			return fmt.Errorf("%s failed: %w\n%s", strings.Join(s, " "), e, strings.TrimSpace(out))
		}
	}
	return nil
}

// DiffBOM lists the Go, toolchain, CI and kit pin changes between two BOMs.
func DiffBOM(a, b *bom.BOM) []string {
	var out []string
	cmp := func(section string, x, y map[string]string) {
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var names []string
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			switch {
			case x[k] == y[k]:
			case x[k] == "":
				out = append(out, fmt.Sprintf("%s %s: added at %s", section, k, y[k]))
			case y[k] == "":
				out = append(out, fmt.Sprintf("%s %s: removed (was %s)", section, k, x[k]))
			default:
				out = append(out, fmt.Sprintf("%s %s: %s -> %s", section, k, x[k], y[k]))
			}
		}
	}
	cmp("go", map[string]string{"floor": a.Toolchain.GoFloor, "min": a.Toolchain.GoMin}, map[string]string{"floor": b.Toolchain.GoFloor, "min": b.Toolchain.GoMin})
	cmp("libs", a.Libs, b.Libs)
	cmp("third_party", a.ThirdParty, b.ThirdParty)
	cmp("ci", a.CI, b.CI)
	cmp("kits", a.Kits, b.Kits)
	return out
}
