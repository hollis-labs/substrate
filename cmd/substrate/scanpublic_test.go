package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/scripts"
)

// Seeded values are assembled at run time so this file does not match the rules
// it tests.
var (
	seededKey = "AKIA" + "IOSFODNN7EXAMPLE"
	seededIP  = "10." + "0.0." + "5"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
}

// repoWith makes a committed repository holding the given files.
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
	return dir
}

func scan(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := scanPublic(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestScanPublicCleanRepoPasses(t *testing.T) {
	requirePython(t)
	repo := repoWith(t, map[string]string{"README.md": "hello, see https://example.test/docs and 192.0.2.7\n"})
	code, out, errs := scan(t, "--no-gitleaks", "--repo", repo, "--ref", "HEAD")
	if code != 0 || !strings.Contains(out, "RESULT: clean") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
}

func TestScanPublicFindsSeededSecretAndHost(t *testing.T) {
	requirePython(t)
	repo := repoWith(t, map[string]string{"a.txt": "key " + seededKey + "\nhost " + seededIP + "\n"})
	code, out, _ := scan(t, "--no-gitleaks", "--repo", repo, "--ref", "HEAD")
	if code != 1 {
		t.Fatalf("code=%d, want 1; out=%s", code, out)
	}
	for _, want := range []string{"aws-access-key", "internal-ip", "NOT clean"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, seededKey) {
		t.Errorf("secret value printed unredacted:\n%s", out)
	}
}

// Without --repo the scan covers the current directory, so the CI line needs
// only --ref HEAD.
func TestScanPublicDefaultsToCurrentDirectory(t *testing.T) {
	requirePython(t)
	repo := repoWith(t, map[string]string{"a.txt": seededKey + "\n"})
	t.Chdir(repo)
	if code, out, _ := scan(t, "--no-gitleaks", "--ref", "HEAD"); code != 1 || !strings.Contains(out, "aws-access-key") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	clean := repoWith(t, map[string]string{"a.txt": "nothing\n"})
	t.Chdir(clean)
	if code, out, _ := scan(t, "--no-gitleaks", "--ref", "HEAD"); code != 0 {
		t.Fatalf("clean repo: code=%d out=%s", code, out)
	}
}

func TestScanPublicAllowlistFilesRespected(t *testing.T) {
	requirePython(t)
	files := map[string]string{
		"a.txt":     "key " + seededKey + "\n",
		"docs/b.md": "ip " + seededIP + "\n",
	}
	// Both allowlist forms an app repo uses: the root file and allow.d/*.allow.
	files["scan-public.allow"] = "aws-access-key a.txt fixture for the example\n"
	files["scan-public.allow.d/docs.allow"] = "internal-ip docs/*.md documentation example\n"
	repo := repoWith(t, files)
	code, out, errs := scan(t, "--no-gitleaks", "--repo", repo)
	if code != 0 || !strings.Contains(out, "allowlisted") {
		t.Fatalf("allowlisted findings should pass and still be shown: code=%d out=%s err=%s", code, out, errs)
	}
	// --no-allowlist shows the raw findings again.
	if code, out, _ := scan(t, "--no-gitleaks", "--repo", repo, "--no-allowlist"); code != 1 {
		t.Fatalf("--no-allowlist: code=%d out=%s", code, out)
	}
	// An entry for another rule does not excuse the finding.
	repo2 := repoWith(t, map[string]string{"a.txt": seededKey + "\n", "scan-public.allow": "internal-ip a.txt wrong rule\n"})
	if code, out, _ := scan(t, "--no-gitleaks", "--repo", repo2); code != 1 {
		t.Fatalf("wrong-rule allow entry: code=%d out=%s", code, out)
	}
}

func TestScanPublicUsageErrorIsTwo(t *testing.T) {
	requirePython(t)
	if code, _, _ := scan(t, "--no-such-flag"); code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
}

func TestScanPublicNoPythonIsTwo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, errs := scan(t, "--ref", "HEAD")
	if code != 2 || !strings.Contains(errs, "python3") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

// python3 -I: PYTHON* variables are ignored, so a planted module on PYTHONPATH
// is not imported by the scanner.
func TestScanPublicRunsIsolated(t *testing.T) {
	requirePython(t)
	hostile := t.TempDir()
	marker := filepath.Join(hostile, "ran")
	if err := os.WriteFile(filepath.Join(hostile, "json.py"), []byte("open("+quote(marker)+",'w').close()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", hostile)
	repo := repoWith(t, map[string]string{"a.txt": "nothing\n"})
	if code, out, errs := scan(t, "--no-gitleaks", "--repo", repo, "--ref", "HEAD"); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("scanner imported a module from PYTHONPATH: not run with -I")
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

// One source: the embedded script is the file in scripts/, byte for byte.
func TestEmbeddedScriptIsTheScriptsFile(t *testing.T) {
	disk, err := os.ReadFile(filepath.Join("..", "..", "scripts", "scan-public"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disk, scripts.ScanPublic) || len(disk) == 0 {
		t.Fatal("embedded scan-public differs from scripts/scan-public")
	}
}
