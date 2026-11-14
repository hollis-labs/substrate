package bom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNPMMissing is returned by CheckFrontend when the npm ls step is wanted and
// npm is not on PATH. CI should fail on it; a developer without node passes
// FrontendOptions.SkipNPMLs.
var ErrNPMMissing = errors.New("npm is not on PATH")

// FrontendOptions tune CheckFrontend.
type FrontendOptions struct {
	// Dir is the frontend directory (holds package.json and package-lock.json).
	Dir string
	// SkipNPMLs skips running `npm ls`; the lockfile is still analysed in Go.
	SkipNPMLs bool
}

type lockPkg struct {
	Name            string                             `json:"name"`
	Version         string                             `json:"version"`
	Link            bool                               `json:"link"`
	Resolved        string                             `json:"resolved"`
	Dependencies    map[string]string                  `json:"dependencies"`
	DevDependencies map[string]string                  `json:"devDependencies"`
	Peers           map[string]string                  `json:"peerDependencies"`
	PeersMeta       map[string]struct{ Optional bool } `json:"peerDependenciesMeta"`
	OptionalDeps    map[string]string                  `json:"optionalDependencies"`
}

type lockFile struct {
	LockfileVersion int                `json:"lockfileVersion"`
	Packages        map[string]lockPkg `json:"packages"`
}

// CheckFrontend holds an app's frontend to the BOM kits. It reads only
// package.json and package-lock.json (lockfile version 2 or 3), so it needs no
// network and no node_modules:
//
//  1. every pinned kit named in package.json is the BOM's exact version, and the
//     lockfile's top-level copy of it is too;
//  2. every dependency or peer range on a pinned kit, anywhere in the lockfile
//     tree, is met by the copy node resolution would find (npm ls reports the
//     same as invalid or missing);
//  3. no pinned kit is installed a second time, nested, at another version.
//
// It also runs `npm ls --all --package-lock-only` and fails on any problem it
// reports for a pinned kit, unless SkipNPMLs. Known incoherences listed in
// kit_exceptions ("<kit>><dependency>") are returned as accepted notes.
func CheckFrontend(b *BOM, o FrontendOptions) (diffs []Diff, accepted []string, err error) {
	pkgPath := filepath.Join(o.Dir, "package.json")
	lockPath := filepath.Join(o.Dir, "package-lock.json")
	for _, p := range []string{pkgPath, lockPath} {
		if _, serr := os.Stat(p); serr != nil {
			return nil, nil, fmt.Errorf("frontend check: %s not found (an app frontend needs package.json and a committed package-lock.json)", p)
		}
	}
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, nil, err
	}
	var pj struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Peers           map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(raw, &pj); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", pkgPath, err)
	}
	lraw, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, nil, err
	}
	var lock lockFile
	if err := json.Unmarshal(lraw, &lock); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", lockPath, err)
	}
	if len(lock.Packages) == 0 {
		return nil, nil, fmt.Errorf("%s: no \"packages\" map (lockfileVersion %d); regenerate it with npm 7 or newer", lockPath, lock.LockfileVersion)
	}

	add := func(file, msg string) { diffs = append(diffs, Diff{file, msg}) }
	note := func(key, msg string) bool {
		if why, ok := b.KitExceptions[key]; ok {
			accepted = append(accepted, msg+" (accepted by kit_exceptions "+key+": "+why+")")
			return true
		}
		return false
	}

	// 1. top level.
	for _, deps := range []map[string]string{pj.Dependencies, pj.DevDependencies, pj.Peers} {
		for n, v := range deps {
			if pin, ok := b.Kits[n]; ok && v != pin {
				add("package.json", fmt.Sprintf("%s is %q, but the BOM pins %s exactly (no ^ or ~)", n, v, pin))
			}
		}
	}
	for n, pin := range b.Kits {
		e, ok := lock.Packages["node_modules/"+n]
		if !ok {
			continue
		}
		if e.Version != pin {
			add("package-lock.json", fmt.Sprintf("node_modules/%s is %s, but the BOM pins %s", n, e.Version, pin))
		}
	}

	// 2. ranges on pinned kits, resolved the way node does.
	paths := make([]string, 0, len(lock.Packages))
	for p := range lock.Packages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		e := lock.Packages[p]
		if e.Link {
			continue
		}
		owner := pkgName(p, e)
		where := p
		if p == "" {
			where = "the app"
		}
		type edge struct {
			dep, rng string
			optional bool
		}
		var edges []edge
		for d, r := range e.Dependencies {
			edges = append(edges, edge{d, r, false})
		}
		for d, r := range e.OptionalDeps {
			edges = append(edges, edge{d, r, true})
		}
		for d, r := range e.Peers {
			edges = append(edges, edge{d, r, e.PeersMeta[d].Optional})
		}
		if p == "" {
			for d, r := range e.DevDependencies {
				edges = append(edges, edge{d, r, false})
			}
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].dep < edges[j].dep })
		for _, ed := range edges {
			if _, pinned := b.Kits[ed.dep]; !pinned {
				continue
			}
			got, at, found := resolve(lock.Packages, p, ed.dep)
			if !found {
				if ed.optional {
					continue
				}
				msg := fmt.Sprintf("%s (%s) needs %s %s, which is not installed anywhere above it", owner, where, ed.dep, ed.rng)
				if !note(owner+">"+ed.dep, msg) {
					add("package-lock.json", "missing: "+msg)
				}
				continue
			}
			ok, serr := satisfies(got.Version, ed.rng)
			if serr != nil {
				return nil, nil, fmt.Errorf("%s: %s range %q on %s: %w", lockPath, owner, ed.rng, ed.dep, serr)
			}
			if !ok {
				msg := fmt.Sprintf("%s (%s) needs %s %s, but %s is installed at %s", owner, where, ed.dep, ed.rng, got.Version, at)
				if !note(owner+">"+ed.dep, msg) {
					add("package-lock.json", "invalid: "+msg)
				}
			}
		}
	}

	// 3. a pinned kit installed again, nested, at another version.
	for _, p := range paths {
		i := strings.LastIndex(p, "node_modules/")
		if i <= 0 {
			continue // top level, handled above
		}
		name := p[i+len("node_modules/"):]
		pin, pinned := b.Kits[name]
		if !pinned {
			continue
		}
		e := lock.Packages[p]
		if e.Link || e.Version == pin {
			continue
		}
		parentPath := strings.TrimSuffix(p[:i], "/")
		parent := pkgName(parentPath, lock.Packages[parentPath])
		msg := fmt.Sprintf("%s is installed a second time at %s (version %s, the BOM pins %s), nested under %s", name, p, e.Version, pin, parent)
		if !note(parent+">"+name, msg) {
			add("package-lock.json", "duplicate: "+msg)
		}
	}

	// npm ls, for what the Go analysis cannot see.
	if !o.SkipNPMLs {
		probs, nerr := npmLsProblems(o.Dir, b)
		if nerr != nil {
			return diffs, accepted, nerr
		}
		for _, pr := range probs {
			add("npm ls", pr)
		}
	}

	sort.SliceStable(diffs, func(i, j int) bool {
		if diffs[i].File != diffs[j].File {
			return diffs[i].File < diffs[j].File
		}
		return diffs[i].Msg < diffs[j].Msg
	})
	sort.Strings(accepted)
	return diffs, accepted, nil
}

// pkgName is the package name at a lockfile path: the entry's own name, else the
// last node_modules segment. The root entry is the app.
func pkgName(path string, e lockPkg) string {
	if path == "" {
		if e.Name != "" {
			return e.Name
		}
		return "the app"
	}
	if i := strings.LastIndex(path, "node_modules/"); i >= 0 {
		return path[i+len("node_modules/"):]
	}
	if e.Name != "" {
		return e.Name
	}
	return path
}

// resolve finds dep the way node resolution does for the package at path: in its
// own node_modules, then in each enclosing one. It follows workspace links.
func resolve(pk map[string]lockPkg, path, dep string) (lockPkg, string, bool) {
	p := path
	for {
		cand := "node_modules/" + dep
		if p != "" {
			cand = p + "/node_modules/" + dep
		}
		if e, ok := pk[cand]; ok {
			if e.Link {
				if t, ok := pk[e.Resolved]; ok {
					return t, cand, true
				}
			}
			return e, cand, true
		}
		if p == "" {
			return lockPkg{}, "", false
		}
		i := strings.LastIndex(p, "/node_modules/")
		if i < 0 {
			p = "" // a top-level package or a workspace folder: resolution continues at the root
			continue
		}
		p = p[:i]
	}
}

type lsNode struct {
	Version      string            `json:"version"`
	Invalid      any               `json:"invalid"`
	Missing      bool              `json:"missing"`
	Extraneous   bool              `json:"extraneous"`
	Problems     []string          `json:"problems"`
	Dependencies map[string]lsNode `json:"dependencies"`
}

// npmLsProblems runs npm ls against the lockfile alone (no network, no
// node_modules) and returns what it reports for a pinned kit.
func npmLsProblems(dir string, b *BOM) ([]string, error) {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return nil, fmt.Errorf("%w: install node/npm, or pass --no-npm-ls to analyse the lockfile in Go only", ErrNPMMissing)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, npm, "ls", "--all", "--json", "--package-lock-only", "--offline", "--no-audit", "--no-fund")
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("npm ls timed out after 90s in %s", dir)
	}
	var root lsNode
	if jerr := json.Unmarshal(out.Bytes(), &root); jerr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("npm ls failed in %s: %v: %s", dir, runErr, strings.TrimSpace(errb.String()))
		}
		return nil, fmt.Errorf("npm ls: unreadable output: %w", jerr)
	}
	var probs []string
	seen := map[string]bool{}
	pinned := func(s string) bool {
		for n := range b.Kits {
			if strings.Contains(s, n) {
				return true
			}
		}
		return false
	}
	var walk func(trail, parent string, n lsNode)
	walk = func(trail, parent string, n lsNode) {
		names := make([]string, 0, len(n.Dependencies))
		for k := range n.Dependencies {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			c := n.Dependencies[k]
			at := trail + " > " + k
			_, excepted := b.KitExceptions[parent+">"+k]
			if _, ok := b.Kits[k]; ok && !excepted && (c.Invalid != nil || c.Missing || c.Extraneous) {
				kind := "invalid"
				switch {
				case c.Missing:
					kind = "missing"
				case c.Extraneous:
					kind = "extraneous"
				}
				s := fmt.Sprintf("%s: %s %s", kind, strings.TrimPrefix(at, " > "), c.Version)
				if !seen[s] {
					seen[s] = true
					probs = append(probs, s)
				}
			}
			walk(at, k, c)
		}
	}
	walk("", "", root)
	for _, pr := range root.Problems {
		if pinned(pr) {
			s := "npm ls: " + pr
			if !seen[s] {
				seen[s] = true
				probs = append(probs, s)
			}
		}
	}
	sort.Strings(probs)
	return probs, nil
}
