// Package logging is the logging module: one slog setup, level and format from config.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/hollis-labs/substrate/module"
)

// Key under which the configured *slog.Logger is published.
const Key = "logging.logger"

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "logging" }

func (mod) Start(_ context.Context, app *module.App) error {
	cfg, _ := app.Config["logging"].(map[string]any)
	var level slog.Level
	if s, _ := cfg["level"].(string); s != "" {
		if err := level.UnmarshalText([]byte(s)); err != nil {
			return fmt.Errorf("invalid level %q: %w", s, err)
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, opts)
	if f, _ := cfg["format"].(string); f == "text" {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	l := slog.New(h).With("app", app.Name)
	slog.SetDefault(l)
	app.Set(Key, l)
	return nil
}

func (mod) Stop(context.Context) error { return nil }
