package boot_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/config"
)

// The after-config hook sees the override already applied and can change values
// the next module reads.
func TestAfterConfigHookSeesOverrideAndMayRewrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.yaml")
	if err := os.WriteFile(p, []byte("httphost:\n  addr: \"127.0.0.1:1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	var seen string
	runs := 0
	run, err := boot.StartWith(context.Background(), "t", []string{"config"}, `{"httphost":{"addr":"x"}}`, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			runs++
			seen = config.String(app, "httphost", "addr", "")
			app.Config["httphost"].(map[string]any)["addr"] = "rewritten"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	if seen != "127.0.0.1:1" || runs != 1 {
		t.Fatalf("hook saw %q, ran %d times", seen, runs)
	}
	if got := config.String(run.App, "httphost", "addr", ""); got != "rewritten" {
		t.Fatalf("rewrite lost: %q", got)
	}
}

func TestStartWithUnknownHookModuleFails(t *testing.T) {
	_, err := boot.StartWith(context.Background(), "t", []string{"config"}, "", boot.Options{
		Before: map[string]boot.Hook{"databse": func(context.Context, *module.App) error { return nil }},
	})
	if err == nil {
		t.Fatal("typo'd module name must be an error")
	}
}
