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
	folio := fs.String("folio", "", "Folio's platform-pins.yaml: a directory or file (check also accepts an http(s) URL)")
	_ = fs.Parse(args[1:])

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
		nd, err := bom.CheckNPM(b, "")
		if err != nil {
			fail(err)
		}
		ds = append(ds, nd...)
	}
	for _, d := range ds {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(ds) > 0 {
		os.Exit(1)
	}
	fmt.Println("pins match the bom")
}
