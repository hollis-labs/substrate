package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/substrate/bom"
)

// pins implements `substrate pins check|sync`. The BOM is the source; go.mod,
// the CI workflow and (with --folio) Folio's platform-pins.yaml follow it.
func pins(args []string) {
	if len(args) < 1 || (args[0] != "check" && args[0] != "sync") {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	verb := args[0]
	fs := flag.NewFlagSet("pins "+verb, flag.ExitOnError)
	root := fs.String("root", ".", "substrate repo root (holds bom/bom.yaml, go.mod, .github/workflows/ci.yml)")
	npm := fs.Bool("npm", false, "check also that every pinned kit version is published (network)")
	app := fs.String("app", "", "check or sync an app's go.mod (directory) against this BOM; the BOM comes from the Substrate version the app requires")
	frontend := fs.Bool("frontend", false, "with --app: also hold the app's frontend (package.json, package-lock.json) to the BOM kits: exact top-level pins, no invalid range on a pinned kit, no nested copy at another version, and `npm ls` clean")
	frontendDir := fs.String("frontend-dir", "frontend", "with --frontend: the frontend directory, relative to --app")
	noNPMLs := fs.Bool("no-npm-ls", false, "with --frontend: do not run `npm ls` (the lockfile is still analysed); the default fails when npm is not installed")
	bomFile := fs.String("bom", "", "with --app: use this bom.yaml instead of the one the app's Substrate release carries")
	folio := fs.String("folio", "", "Folio's platform-pins.yaml: a directory or file (check also accepts an http(s) URL)")
	_ = fs.Parse(args[1:])

	if *frontend && *app == "" {
		fail(fmt.Errorf("--frontend needs --app DIR"))
	}
	if *frontend && verb != "check" {
		fail(fmt.Errorf("--frontend only applies to `pins check`"))
	}
	if *app != "" {
		pinsApp(verb, *app, *bomFile, *frontend, filepath.Join(*app, *frontendDir), *noNPMLs)
		return
	}
	b, err := bom.Load(filepath.Join(*root, "bom", "bom.yaml"))
	if err != nil {
		fail(err)
	}
	t := bom.Targets{
		GoMod: filepath.Join(*root, "go.mod"),
		Workflows: []string{
			filepath.Join(*root, ".github", "workflows", "ci.yml"),
			filepath.Join(*root, ".github", "workflows", "build.yml"),
		},
	}
	if *folio != "" {
		t.Folio = bom.FolioFile(*folio)
	}
	if verb == "sync" {
		changed, err := bom.Sync(b, t)
		for _, c := range changed {
			fmt.Println("updated", c)
		}
		if err != nil {
			fail(err)
		}
		if len(changed) == 0 {
			fmt.Println("already in step with the bom")
		}
		for _, c := range changed {
			if filepath.Base(c) == "go.mod" {
				fmt.Println("go.mod changed: run `go mod tidy`")
			}
		}
		return
	}
	ds, err := bom.Check(b, t)
	if err != nil {
		fail(err)
	}
	if *npm {
		nd, accepted, err := bom.CheckNPM(b, "")
		if err != nil {
			fail(err)
		}
		ds = append(ds, nd...)
		for _, a := range accepted {
			fmt.Println(a)
		}
	}
	for _, d := range ds {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(ds) > 0 {
		os.Exit(1)
	}
	fmt.Println("pins match the bom")
}

// pinsApp holds an app's go.mod to the BOM of the Substrate release it requires.
func pinsApp(verb, dir, bomFile string, frontend bool, feDir string, noNPMLs bool) {
	bomPath := bomFile
	if bomPath == "" {
		root, err := goList(dir, "-m", "-f", "{{.Dir}}", "github.com/hollis-labs/substrate")
		if err != nil || root == "" {
			fail(fmt.Errorf("find the substrate module the app requires: %v", err))
		}
		bomPath = filepath.Join(root, "bom", "bom.yaml")
	}
	b, err := bom.Load(bomPath)
	if err != nil {
		fail(err)
	}
	goMod := filepath.Join(dir, "go.mod")
	if verb == "sync" {
		changed, err := bom.SyncApp(b, goMod)
		if changed {
			fmt.Println("updated", goMod, "(run `go mod tidy`)")
		}
		if err != nil {
			fail(err)
		}
		if !changed {
			fmt.Println("already in step with the bom")
		}
		return
	}
	ds, err := bom.CheckApp(b, goMod)
	if err != nil {
		fail(err)
	}
	if frontend {
		fd, accepted, ferr := bom.CheckFrontend(b, bom.FrontendOptions{Dir: feDir, SkipNPMLs: noNPMLs})
		if ferr != nil {
			fail(ferr)
		}
		ds = append(ds, fd...)
		for _, a := range accepted {
			fmt.Println("accepted:", a)
		}
	}
	for _, d := range ds {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(ds) > 0 {
		os.Exit(1)
	}
	if frontend {
		fmt.Println("app pins and frontend kits match the bom")
		return
	}
	fmt.Println("app pins match the bom")
}
