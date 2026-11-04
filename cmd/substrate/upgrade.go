package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hollis-labs/substrate/upgrade"
)

// upgradeCmd implements `substrate upgrade`: move an app to another Substrate
// release, or report what that would do.
func upgradeCmd(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	dir := fs.String("dir", ".", "the app's directory (holds go.mod)")
	to := fs.String("to", "", "target version or module query (default: latest release)")
	manifest := fs.String("f", "substrate.yaml", "manifest to validate against the target release, relative to -dir; \"\" skips")
	dry := fs.Bool("dry-run", false, "report the plan, change nothing")
	allow := fs.Bool("allow-unmigrated", false, "proceed although a breaking change has no mechanical migration")
	_ = fs.Parse(args)

	o := upgrade.Options{Dir: *dir, To: *to, DryRun: *dry, AllowUnmigrated: *allow, Run: runCmd}
	if *manifest != "" {
		if _, err := os.Stat(filepath.Join(*dir, *manifest)); err == nil {
			o.Manifest = *manifest
		}
	}
	p, err := upgrade.Make(o)
	if err != nil {
		fail(err)
	}
	p.Report(os.Stdout)
	if err := upgrade.Apply(p, o); err != nil {
		fail(err)
	}
	if p.Kind != upgrade.Same && !*dry {
		fmt.Println("upgraded and verified (manifest valid, app builds)")
	}
}

func runCmd(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}
