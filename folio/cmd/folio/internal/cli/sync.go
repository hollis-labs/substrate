package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/tools/folio/service"
)

func syncCmd(bundledFS fs.FS, version string) *cobra.Command {
	var plan, asJSON bool
	var theirsTo string
	cmd := &cobra.Command{
		Use:   "sync <target-dir>",
		Short: "Bring a project up to its preset without overwriting what was written there",
		Long: `sync re-renders the preset recorded in <target-dir>/.folio.yaml (the version
folio has now) and applies what inspect reports, file by file:

  preset_updated    written: the file was as generated
  added_upstream    written, unless the project already has its own file there
  locally_modified  kept as it is
  conflict          kept as it is and reported; --theirs-to writes the new
                    rendering beside it (README.md.folio-new) to merge by hand
  removed_upstream  kept, reported
  missing_locally   not restored, reported

A file under a sync rule with policy skip is never written, whatever its state.
A conflict under a rule with policy overwrite takes the preset's copy.

--plan prints what sync would do and writes nothing. --input sets an input a
newer preset version needs (or changes one). Without --plan, .folio.yaml
records the new versions, inputs and digests, and a sync_history entry.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pairs, err := cmd.Flags().GetStringArray("input")
			if err != nil {
				return err
			}
			kv, err := parseFlagPairs(pairs)
			if err != nil {
				return &service.Error{Code: service.ErrInputInvalid, Message: err.Error()}
			}
			inputs := map[string]any{}
			for k, v := range kv {
				inputs[k] = v
			}
			svc := service.New(service.Options{
				BundledFS:    bundledFS,
				BundledRoot:  "presets",
				FolioVersion: version,
			})
			res, err := svc.Sync(service.SyncOptions{TargetDir: args[0], Plan: plan, Inputs: inputs, TheirsTo: theirsTo})
			if err != nil && len(res.Files) == 0 {
				return err
			}
			if asJSON {
				if jerr := writeSyncJSON(cmd, args[0], res); jerr != nil {
					return jerr
				}
			} else {
				writeSyncTable(cmd, args[0], res)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "show what sync would do and write nothing")
	cmd.Flags().StringVar(&theirsTo, "theirs-to", "", "for a conflict, write the preset's new rendering beside the file with this suffix (e.g. .folio-new)")
	cmd.Flags().StringArray("input", nil, "Set an input (repeatable): --input key=value")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

type syncJSON struct {
	Target      string         `json:"target"`
	Preset      string         `json:"preset"`
	FromVersion string         `json:"from_version"`
	ToVersion   string         `json:"to_version"`
	Plan        bool           `json:"plan"`
	Files       []syncFileJSON `json:"files"`
	Conflicts   int            `json:"conflicts"`
	Warnings    []string       `json:"warnings"`
}

type syncFileJSON struct {
	Path   string `json:"path"`
	Preset string `json:"preset"`
	Status string `json:"status"`
	Action string `json:"action"`
	Policy string `json:"policy,omitempty"`
	Theirs string `json:"theirs,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func writeSyncJSON(cmd *cobra.Command, target string, res service.SyncResult) error {
	out := syncJSON{Target: target, Preset: res.RootPreset, FromVersion: res.FromVersion, ToVersion: res.ToVersion,
		Plan: res.Plan, Files: make([]syncFileJSON, 0, len(res.Files)), Conflicts: len(res.Conflicts()), Warnings: res.Warnings}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	for _, f := range res.Files {
		out.Files = append(out.Files, syncFileJSON{Path: f.Path, Preset: f.Preset, Status: string(f.Status), Action: string(f.Action), Policy: f.Policy, Theirs: f.Theirs, Detail: f.Detail})
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeSyncTable(cmd *cobra.Command, target string, res service.SyncResult) {
	w := cmd.OutOrStdout()
	name := target
	if abs, err := filepath.Abs(target); err == nil {
		name = filepath.Base(abs)
	}
	verb := "folio sync"
	if res.Plan {
		verb = "folio sync --plan"
	}
	fmt.Fprintf(w, "%s: %s (preset %s %s -> %s)\n\n", verb, name, res.RootPreset, res.FromVersion, res.ToVersion)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  PATH\tSTATUS\tACTION\tNOTE")
	for _, f := range res.Files {
		note := f.Detail
		if f.Theirs != "" {
			note += "; new rendering at " + f.Theirs
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", f.Path, f.Status, f.Action, note)
	}
	_ = tw.Flush()
	if c := res.Conflicts(); len(c) > 0 {
		fmt.Fprintf(w, "\n%d conflict(s) left as they are: merge them by hand", len(c))
		if c[0].Theirs == "" {
			fmt.Fprint(w, " (--theirs-to .folio-new writes the preset's version beside each)")
		}
		fmt.Fprintln(w)
	}
	for _, warn := range res.Warnings {
		fmt.Fprintf(w, "\nwarning: %s", warn)
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintln(w)
	}
	if res.Plan {
		fmt.Fprintln(w, "\nNothing was written (--plan).")
	}
}
