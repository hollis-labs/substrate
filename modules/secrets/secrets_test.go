package secrets_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/logging"
	"github.com/hollis-labs/substrate/modules/secrets"
)

const value = "tok_9f8e7d6c5b4a39281706f5e4d3c2b1a0"

func start(t *testing.T, cfg string) *module.App {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"logging", "secrets"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run.App
}

func TestResolvedValueIsRedactedFromLogsAndText(t *testing.T) {
	t.Setenv("T_API_TOKEN", value)

	// The logging module builds its handler on os.Stderr at Start; capture it.
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = orig })
	app := start(t, `{"secrets":{"env":true}}`)
	os.Stderr = orig

	res, err := secrets.Resolver(app).Resolve(context.Background(), "env://T_API_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if res.Secret.Reveal() != value {
		t.Fatal("wrong value resolved")
	}
	if got := secrets.Redactor(app).Text("auth failed for " + value); strings.Contains(got, value) {
		t.Fatalf("resolved value leaked from Text: %s", got)
	}
	v, _ := app.Value(logging.Key)
	v.(*slog.Logger).Info("calling upstream", "detail", "token="+value)
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "calling upstream") {
		t.Fatalf("nothing was logged: %q", out)
	}
	if strings.Contains(string(out), value) {
		t.Fatalf("resolved value leaked into logs: %s", out)
	}
}

func TestEnvReferencesAreOffByDefault(t *testing.T) {
	t.Setenv("T_API_TOKEN", value)
	app := start(t, `{}`)
	if _, err := secrets.Resolver(app).Resolve(context.Background(), "env://T_API_TOKEN"); err == nil {
		t.Fatal("env:// must be refused unless config enables it")
	}
}
