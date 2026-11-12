//go:build unix

package identity_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/identity"
)

const (
	envChildDir     = "SUBSTRATE_IDTEST_CHILD_DIR"
	envChildBarrier = "SUBSTRATE_IDTEST_CHILD_BARRIER"
)

func identityCfg(dir string) string {
	return fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "enforce"}, "identity": {"key_file": %q, "bcrypt_cost": 4}}`,
		filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
}

var identityModules = []string{"health", "logging", "auth", "database", "httphost", "identity"}

// TestMain lets the test binary act as one of the processes of the first-start
// race: with envChildDir set it waits for the barrier file, starts identity on that
// data directory, mints a token and prints "<kid> <token>".
func TestMain(m *testing.M) {
	if dir := os.Getenv(envChildDir); dir != "" {
		os.Exit(runIdentityChild(dir, os.Getenv(envChildBarrier)))
	}
	os.Exit(m.Run())
}

func runIdentityChild(dir, barrier string) int {
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		time.Sleep(200 * time.Microsecond)
	}
	run, err := boot.Start(context.Background(), "demo", identityModules, identityCfg(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	svc := identity.From(run.App)
	tok, err := svc.MintForTest("user:probe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s %s\n", svc.SigningKey().Active.KID, tok)
	_ = run.Stop(context.Background())
	return 0
}

// Several real processes starting identity at the same moment on one NEW data
// directory must all end with the same signing key: a token minted by any of them
// verifies against the key on disk (so in every other process), and nothing fails.
func TestConcurrentFirstStartSharesOneSigningKey(t *testing.T) {
	rounds, procs := 8, 3
	if testing.Short() {
		rounds = 2
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rounds; r++ {
		dir := t.TempDir()
		barrier := filepath.Join(dir, "go")
		cmds := make([]*exec.Cmd, procs)
		outs := make([]*strings.Builder, procs)
		errs := make([]*strings.Builder, procs)
		for i := range cmds {
			c := exec.Command(exe, "-test.run=^$")
			c.Env = append(os.Environ(), envChildDir+"="+dir, envChildBarrier+"="+barrier)
			outs[i], errs[i] = &strings.Builder{}, &strings.Builder{}
			c.Stdout, c.Stderr = outs[i], errs[i]
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			cmds[i] = c
		}
		time.Sleep(300 * time.Millisecond) // let every child reach the barrier loop
		if err := os.WriteFile(barrier, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		kids, toks := map[string]bool{}, []string{}
		for i, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Errorf("round %d process %d: %v: %s", r, i, err, errs[i])
				continue
			}
			f := strings.Fields(outs[i].String())
			if len(f) != 2 {
				t.Errorf("round %d process %d: unexpected output %q", r, i, outs[i])
				continue
			}
			kids[f[0]] = true
			toks = append(toks, f[1])
		}
		if len(kids) > 1 {
			t.Errorf("round %d: processes ended with %d different signing keys: %v", r, len(kids), kids)
		}
		// Whatever is on disk now is the key every token must verify against.
		a := startIn(t, dir, "enforce", "")
		for i, tok := range toks {
			if _, err := a.svc.Verify(context.Background(), tok); err != nil {
				t.Errorf("round %d: token %d does not verify against the key on disk: %v", r, i, err)
			}
		}
		if len(kids) > 0 && !kids[a.svc.SigningKey().Active.KID] {
			t.Errorf("round %d: key on disk has kid %s, processes used %v", r, a.svc.SigningKey().Active.KID, kids)
		}
	}
}
