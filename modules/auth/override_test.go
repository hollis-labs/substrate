package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/config"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// denylist_interval_seconds: 1, a bare integer in the SUBSTRATE_CONFIG override
// file, takes effect: a revoked token stops working within a few seconds. Ignored,
// the poller would keep its 30s default and the token would outlive the wait.
func TestDenylistIntervalFromAnOverrideFileTakesEffect(t *testing.T) {
	is := newIssuer(t)
	p := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(p, []byte("auth:\n  denylist_interval_seconds: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	cfg := `{"auth":{"mode":"enforce","jwks_url":"` + is.srv.URL + `/jwks.json","denylist_url":"` + is.srv.URL + `/denylist"}}`
	run, err := boot.Start(context.Background(), "t", []string{"config", "health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	h, _ := httphost.Handler(run.App, httphost.Options{})
	tok := is.token("t", "svc/worker", "jti-1", "t.read")
	if got := statusOf(h, "/api/me", tok); got != 200 {
		t.Fatalf("a token signed by the issuer: %d", got)
	}
	is.denied.Store([]string{"jti-1"})
	deadline := time.Now().Add(4 * time.Second)
	for statusOf(h, "/api/me", tok) != 401 {
		if time.Now().After(deadline) {
			t.Fatal("a revoked token still works after 4s: denylist_interval_seconds from the override file was ignored")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
