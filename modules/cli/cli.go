// Package cli is the CLI skeleton module: a cobra root command with the
// standard subcommands every Substrate app gets (version, config, health). The
// app adds its own commands to Root(app) and runs it; Substrate does not own
// the app's commands or its main.
//
//	version  module path, version and VCS revision from the build info
//	config   effective module config as YAML, redacted when the secrets module
//	         is started
//	health   runs the health checks in-process and prints the report; exit 1
//	         when degraded
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

// Key under which the root *cobra.Command is published.
const Key = "cli.root"

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "cli" }

func (mod) Start(_ context.Context, app *module.App) error {
	root := &cobra.Command{Use: app.Name, SilenceUsage: true}
	root.AddCommand(versionCmd(app), configCmd(app), healthCmd(app))
	app.Set(Key, root)
	if v, ok := app.Value(pendingKey); ok {
		for _, c := range v.([]*cobra.Command) {
			root.AddCommand(c)
		}
		delete(app.Values, pendingKey)
	}
	return nil
}

const pendingKey = "cli.pending"

// AddCommand adds a command to the app's root, whichever of the calling module
// and the cli module starts first: if the root does not exist yet the command
// is queued and added when cli starts. A module uses it to contribute
// subcommands to every app that selects both. With no cli module selected
// the command is queued and never used.
func AddCommand(app *module.App, c *cobra.Command) {
	if root := Root(app); root != nil {
		root.AddCommand(c)
		return
	}
	l, _ := app.Value(pendingKey)
	list, _ := l.([]*cobra.Command)
	app.Set(pendingKey, append(list, c))
}

func (mod) Stop(context.Context) error { return nil }

// Root returns the root command published by the cli module, or nil.
func Root(app *module.App) *cobra.Command {
	v, _ := app.Value(Key)
	c, _ := v.(*cobra.Command)
	return c
}

func versionCmd(app *module.App) *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print version and build information", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			version, rev := "(devel)", ""
			if bi, ok := debug.ReadBuildInfo(); ok {
				if bi.Main.Version != "" {
					version = bi.Main.Version
				}
				for _, s := range bi.Settings {
					if s.Key == "vcs.revision" {
						rev = s.Value
					}
				}
			}
			_, err := fmt.Fprintf(c.OutOrStdout(), "%s %s %s\n", app.Name, version, rev)
			return err
		},
	}
}

type texter interface{ Text(string) string }

func configCmd(app *module.App) *cobra.Command {
	return &cobra.Command{
		Use: "config", Short: "Print the effective module config (redacted)", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			b, err := yaml.Marshal(app.Config)
			if err != nil {
				return err
			}
			out := string(b)
			if v, ok := app.Value("secrets.redactor"); ok {
				if t, ok := v.(texter); ok {
					out = t.Text(out)
				}
			}
			_, err = fmt.Fprint(c.OutOrStdout(), out)
			return err
		},
	}
}

func healthCmd(app *module.App) *cobra.Command {
	return &cobra.Command{
		Use: "health", Short: "Run health checks and print the report", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			reg := health.From(app)
			if reg == nil {
				return fmt.Errorf("health module is not started")
			}
			rep := reg.Report(c.Context())
			b, _ := json.MarshalIndent(rep, "", "  ")
			if _, err := fmt.Fprintln(c.OutOrStdout(), string(b)); err != nil {
				return err
			}
			if rep.Status != "ok" {
				return fmt.Errorf("health: %s", rep.Status)
			}
			return nil
		},
	}
}
