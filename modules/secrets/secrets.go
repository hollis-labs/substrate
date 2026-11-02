// Package secrets is the secrets module: libs security/secrets references
// (keychain://, file://, op://, helper://, optionally env://) resolved on
// demand, with every resolved value fed to a libs security/redact scope. The
// logger published by the logging module is wrapped so resolved values and
// credential patterns never reach log output. Substrate does not hold or
// custody secrets: values live in the backends the app points at.
//
// Config under "secrets":
//
//	env:       true to allow env://VAR references (off by default).
//	service:   keychain service name (libs default).
//	state_dir: managed file-store directory (libs default).
//
// Requires the logging module so redaction covers log output.
package secrets

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/libs/security/redact"
	libsecrets "github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/logging"
)

// Keys under which the resolver and redactor are published.
const (
	ResolverKey = "secrets.resolver"
	RedactorKey = "secrets.redactor"
)

// scopeSink adapts a redact.Scope to the resolver's ValueSink.
type scopeSink struct{ s *redact.Scope }

func (k scopeSink) Add(values ...string) {
	for _, v := range values {
		k.s.Add("resolved", v)
	}
}

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "secrets" }

func (mod) Start(_ context.Context, app *module.App) error {
	cfg, _ := app.Config["secrets"].(map[string]any)

	scope := redact.NewScope()
	red, err := redact.New(redact.WithScope(scope))
	if err != nil {
		return err
	}
	opts := []libsecrets.Option{libsecrets.WithRedactor(scopeSink{scope})}
	if b, _ := cfg["env"].(bool); b {
		opts = append(opts, libsecrets.WithEnv())
	}
	if s, _ := cfg["service"].(string); s != "" {
		opts = append(opts, libsecrets.WithService(s))
	}
	if s, _ := cfg["state_dir"].(string); s != "" {
		opts = append(opts, libsecrets.WithStateDir(s))
	}

	if v, ok := app.Value(logging.Key); ok {
		if l, ok := v.(*slog.Logger); ok {
			wrapped := slog.New(red.Handler(l.Handler()))
			slog.SetDefault(wrapped)
			app.Set(logging.Key, wrapped)
		}
	}
	app.Set(ResolverKey, libsecrets.New(opts...))
	app.Set(RedactorKey, red)
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// Resolver returns the resolver published by the secrets module, or nil.
func Resolver(app *module.App) *libsecrets.Resolver {
	v, _ := app.Value(ResolverKey)
	r, _ := v.(*libsecrets.Resolver)
	return r
}

// Redactor returns the redactor published by the secrets module, or nil. Use it
// wherever text leaves the process: errors, stored output, diagnostics.
func Redactor(app *module.App) *redact.Redactor {
	v, _ := app.Value(RedactorKey)
	r, _ := v.(*redact.Redactor)
	return r
}
