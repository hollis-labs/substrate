//go:build unix

package identity_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/identity"
)

const (
	envChildDir     = "SUBSTRATE_IDTEST_CHILD_DIR"
	envChildBarrier = "SUBSTRATE_IDTEST_CHILD_BARRIER"
	envChildMode    = "SUBSTRATE_IDTEST_CHILD_MODE" // "setup": run `identity setup` instead of serving
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
		if os.Getenv(envChildMode) == "setup" {
			os.Exit(runSetupChild(dir, os.Getenv(envChildBarrier)))
		}
		os.Exit(runIdentityChild(dir, os.Getenv(envChildBarrier)))
	}
	os.Exit(m.Run())
}

func waitBarrier(barrier string) {
	for {
		if _, err := os.Stat(barrier); err == nil {
			return
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// runSetupChild is the offline bootstrap as the app binary runs it: boot with the
// cli module, then `identity setup` with the password on stdin. It prints
// "<kid> ok".
func runSetupChild(dir, barrier string) int {
	waitBarrier(barrier)
	run, err := boot.Start(context.Background(), "demo", cliModules(false), cliConfig(dir, ""))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	v, ok := run.App.Value("cli.root")
	if !ok {
		fmt.Fprintln(os.Stderr, "no cli root")
		return 1
	}
	root := v.(*cobra.Command)
	root.SetIn(os.Stdin)
	root.SetOut(io.Discard)
	root.SetErr(os.Stderr)
	root.SetArgs(setupArgs())
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s ok\n", identity.From(run.App).SigningKey().Active.KID)
	_ = run.Stop(context.Background())
	return 0
}

func runIdentityChild(dir, barrier string) int {
	waitBarrier(barrier)
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

// `identity setup` on a fresh data directory racing a starting service must also
// end with ONE signing key and a working owner: each process creates the key on
// its own start, before the owner exists, so both reach the exclusive create.
func TestConcurrentSetupAndServiceShareOneSigningKeyAndTheOwnerWorks(t *testing.T) {
	rounds := 8
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
		spawn := func(mode string, stdin io.Reader) (*exec.Cmd, *strings.Builder, *strings.Builder) {
			c := exec.Command(exe, "-test.run=^$")
			c.Env = append(os.Environ(), envChildDir+"="+dir, envChildBarrier+"="+barrier, envChildMode+"="+mode)
			c.Stdin = stdin
			out, errb := &strings.Builder{}, &strings.Builder{}
			c.Stdout, c.Stderr = out, errb
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			return c, out, errb
		}
		svcCmd, svcOut, svcErr := spawn("serve", nil)
		setCmd, setOut, setErr := spawn("setup", pw(secretPassword))
		time.Sleep(300 * time.Millisecond) // let both reach the barrier loop
		if err := os.WriteFile(barrier, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := svcCmd.Wait(); err != nil {
			t.Errorf("round %d service: %v: %s", r, err, svcErr)
		}
		if err := setCmd.Wait(); err != nil {
			t.Errorf("round %d setup: %v: %s", r, err, setErr)
		}
		sf, pf := strings.Fields(svcOut.String()), strings.Fields(setOut.String())
		if len(sf) != 2 || len(pf) != 2 {
			t.Errorf("round %d: unexpected output service %q setup %q", r, svcOut, setOut)
			continue
		}
		if sf[0] != pf[0] {
			t.Errorf("round %d: service and setup ended with different signing keys: %s vs %s", r, sf[0], pf[0])
		}
		// One key on disk, the one both used; the token the service minted verifies
		// against it; and the owner setup created can sign in.
		a := startIn(t, dir, "enforce", "")
		if kid := a.svc.SigningKey().Active.KID; kid != sf[0] {
			t.Errorf("round %d: key on disk has kid %s, the processes used %s", r, kid, sf[0])
		}
		if _, err := a.svc.Verify(context.Background(), sf[1]); err != nil {
			t.Errorf("round %d: the service's token does not verify against the key on disk: %v", r, err)
		}
		code(t, a.login("owner", secretPassword), 200)
		if m, _ := filepath.Glob(filepath.Join(dir, "id.key*")); len(m) != 2 {
			t.Errorf("round %d: expected the key, its jwks and no scratch files, found %v", r, m)
		}
	}
}
