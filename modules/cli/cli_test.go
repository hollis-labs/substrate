package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/secrets"
)

func run(t *testing.T, mods []string, cfg string, args ...string) (string, error) {
	t.Helper()
	r, err := boot.Start(context.Background(), "t", mods, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background()) })
	root := cli.Root(r.App)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestVersionAndHealth(t *testing.T) {
	out, err := run(t, []string{"health", "cli"}, "", "version")
	if err != nil || !strings.HasPrefix(out, "t ") {
		t.Fatalf("version: %q %v", out, err)
	}
	out, err = run(t, []string{"health", "cli"}, "", "health")
	if err != nil || !strings.Contains(out, `"status": "ok"`) {
		t.Fatalf("health: %q %v", out, err)
	}
}

func TestHealthExitsNonZeroWhenDegraded(t *testing.T) {
	r, err := boot.Start(context.Background(), "t", []string{"health", "cli"}, "")
	if err != nil {
		t.Fatal(err)
	}
	health.From(r.App).Add("db", func(context.Context) error { return context.DeadlineExceeded })
	root := cli.Root(r.App)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"health"})
	if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(out.String(), "degraded") {
		t.Fatalf("want degraded error, got %v / %q", err, out.String())
	}
}

func TestConfigIsRedactedWhenSecretsStarted(t *testing.T) {
	const value = "tok_9f8e7d6c5b4a39281706f5e4d3c2b1a0"
	t.Setenv("T_CLI_VALUE", value)
	r, err := boot.Start(context.Background(), "t", []string{"logging", "secrets", "health", "cli"},
		`{"logging":{"level":"warn"},"secrets":{"env":true},"note":{"text":"`+value+`"}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background()) })
	// Resolving teaches the redactor the value; the config command must then hide it.
	if _, err := secrets.Resolver(r.App).Resolve(context.Background(), "env://T_CLI_VALUE"); err != nil {
		t.Fatal(err)
	}
	root := cli.Root(r.App)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), value) {
		t.Fatalf("config leaked a resolved value: %s", out.String())
	}
	if !strings.Contains(out.String(), "level: warn") {
		t.Fatalf("config missing values: %s", out.String())
	}
}
