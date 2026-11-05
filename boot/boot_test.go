package boot_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/config"
)

// A manifest with no config block generates the JSON text "null". With a
// SUBSTRATE_CONFIG override that used to panic with a write to a nil map.
func TestNullConfigWithAnOverrideFileStarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.yaml")
	if err := os.WriteFile(p, []byte("httphost:\n  addr: \"127.0.0.1:1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	for _, cfg := range []string{"null", "", "{}"} {
		run, err := boot.Start(context.Background(), "t", []string{"config"}, cfg)
		if err != nil {
			t.Fatalf("config %q: %v", cfg, err)
		}
		if got := config.String(run.App, "httphost", "addr", ""); got != "127.0.0.1:1" {
			t.Errorf("config %q: override not applied: %q", cfg, got)
		}
		_ = run.Stop(context.Background())
	}
}
