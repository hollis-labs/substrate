// Package cli wires the Cobra command tree for the folio binary. It lives
// under cmd/folio/internal/ so the API surface stays unexported and tests
// can drive Run(...) directly without spawning subprocesses.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/folio"
	"github.com/hollis-labs/substrate/folio/service"
)

// Exit codes follow design doc cli-prompt-flow-v0.md §9.
const (
	ExitOK        = 0
	ExitGeneric   = 1
	ExitUsage     = 2
	ExitCancelled = 130
)

// Run constructs the root command tree, executes it against args, and
// returns the process exit code. bundledFS supplies the read-only
// filesystem holding the bundled presets (typically folio.BundledPresets).
// Pass nil to disable bundled-preset lookup (tests).
func Run(args []string, bundledFS fs.FS, version string) int {
	root := NewRootCmd(bundledFS, version)
	root.SetArgs(args)
	root.SilenceErrors = true
	root.SilenceUsage = true

	err := root.ExecuteContext(context.Background())
	if err == nil {
		return ExitOK
	}
	// SilenceErrors keeps cobra quiet so the CLI controls its own
	// presentation; we still need to surface the error to the user.
	var ce *cancelledError
	if !errors.As(err, &ce) {
		fmt.Fprintln(os.Stderr, "folio: "+err.Error())
	}
	return exitCodeFor(err)
}

// NewRootCmd builds the `folio` Cobra root with all subcommands wired in.
// Exposed for tests that need to invoke commands directly (e.g. in-process
// rather than via Run + os.Args).
func NewRootCmd(bundledFS fs.FS, version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "folio",
		Short: "Preset-driven project scaffolding",
		Long: `folio scaffolds new projects from preset manifests.

A preset bundles a typed inputs schema, computed variables, and a
text/template-rendered file tree. ` + "`folio new`" + ` renders a preset into a target
directory; ` + "`folio plan`" + ` previews the same render without writing.`,
		Version:           version,
		DisableAutoGenTag: true,
	}

	// The pointer is shared by command services and populated after Cobra parses
	// flags. No filesystem is mounted unless explicitly selected for this run.
	var packDirs []string
	basePresets := bundledFS
	mounted := &selectedPresets{FS: bundledFS}
	root.PersistentFlags().StringArrayVar(&packDirs, "preset-pack", nil, "Trusted catalog root containing presets/<id> (repeatable; duplicate IDs fail)")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		packs := make([]fs.FS, 0, len(packDirs))
		for _, dir := range packDirs {
			packs = append(packs, os.DirFS(dir))
		}
		combined, err := folio.WithPresetPacks(basePresets, packs...)
		if err != nil {
			return err
		}
		mounted.FS = combined
		return nil
	}
	bundledFS = mounted

	root.AddCommand(newCmd(bundledFS, version))
	root.AddCommand(makeCmd(bundledFS, version))
	root.AddCommand(planCmd(bundledFS, version))
	root.AddCommand(presetCmd(bundledFS, version))
	root.AddCommand(inspectCmd(bundledFS, version))
	root.AddCommand(deployCmd())
	root.AddCommand(syncCmd(bundledFS, version))

	return root
}

// exitCodeFor maps service errors and Cobra usage errors to the documented
// exit-code table.
func exitCodeFor(err error) int {
	var ce *cancelledError
	if errors.As(err, &ce) {
		return ExitCancelled
	}
	var se *service.Error
	if errors.As(err, &se) {
		switch se.Code {
		case service.ErrInputMissing, service.ErrInputInvalid:
			return ExitUsage
		default:
			return ExitGeneric
		}
	}
	// Cobra surfaces flag/usage errors via the cobra.ErrSubCommandRequired
	// family — they typically embed a UsageError. We treat any non-service
	// error from Execute as a generic failure.
	return ExitGeneric
}

// fprintf writes a formatted line to the command's err stream. Helper so
// CLI code doesn't sprinkle direct os.Stderr writes.
func fprintln(cmd *cobra.Command, msg string) {
	if cmd != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// selectedPresets defers filesystem selection until persistent flags are parsed.
type selectedPresets struct{ fs.FS }

func (s *selectedPresets) Open(name string) (fs.File, error) {
	if s.FS == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return s.FS.Open(name)
}
