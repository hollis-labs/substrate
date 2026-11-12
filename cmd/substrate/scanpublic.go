package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/substrate/scripts"
)

// scanPublic runs the embedded scripts/scan-public with `python3 -I` and
// returns its exit status (0 clean, 1 findings, 2 the scanner could not run).
// Every argument is the script's own: --ref, --repo, --source, --tree,
// --history, --no-gitleaks, --allowlist, --no-allowlist. Without --repo or
// --source the scan covers the current directory, so an app's CI line is
// `substrate scan-public --ref HEAD` and the app's scan-public.allow and
// scan-public.allow.d/*.allow files are found at its root, as with a copied
// script.
func scanPublic(args []string, stdout, stderr io.Writer) int {
	py, err := exec.LookPath("python3")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ERROR: scan-public needs python3 on PATH (the scanner is a Python script); install it and retry")
		return 2
	}
	repo, hasRepo, hasSource := "", false, false
	for i, a := range args {
		switch {
		case a == "--repo" && i+1 < len(args):
			repo, hasRepo = args[i+1], true
		case strings.HasPrefix(a, "--repo="):
			repo, hasRepo = strings.TrimPrefix(a, "--repo="), true
		case a == "--source" || strings.HasPrefix(a, "--source="):
			hasSource = true
		}
	}
	if !hasRepo {
		wd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "ERROR: scan-public:", err)
			return 2
		}
		repo = wd
		if !hasSource {
			args = append([]string{"--repo", wd}, args...)
		}
	}

	dir, err := os.MkdirTemp("", "substrate-scan-public.")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ERROR: scan-public:", err)
		return 2
	}
	defer func() { _ = os.RemoveAll(dir) }()
	script := filepath.Join(dir, "scan-public")
	if err := os.WriteFile(script, scripts.ScanPublic, 0o600); err != nil {
		_, _ = fmt.Fprintln(stderr, "ERROR: scan-public:", err)
		return 2
	}

	// -I: no PYTHON* variables, no user site, no script directory on sys.path, so
	// the scanner imports only the standard library.
	cmd := exec.Command(py, append([]string{"-I", script}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	cmd.Env = os.Environ()
	if !hasSource {
		// The script trusts a reviewed .gitleaksignore of "its own" repository; here
		// that is the repository being scanned, as when the script was copied into it.
		if abs, err := filepath.Abs(repo); err == nil {
			cmd.Env = append(cmd.Env, "SCAN_PUBLIC_OWN_REPO="+abs)
		}
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, "ERROR: scan-public:", err)
		return 2
	}
	return 0
}
