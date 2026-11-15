package cli

import (
	"fmt"
	"github.com/hollis-labs/tools/folio/service"
	"github.com/spf13/cobra"
)

func deployCmd() *cobra.Command {
	parent := &cobra.Command{Use: "deploy", Short: "Render local deployment descriptors (never deploys a service)"}
	var plan, write, overwrite bool
	child := &cobra.Command{Use: "render <project-dir>", Short: "Project project.yaml settings.runtime into fleet/Cerberus files", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if plan && write {
			return fmt.Errorf("choose --plan or --write")
		}
		if overwrite && !write {
			return fmt.Errorf("--overwrite requires --write after reviewing a plan")
		}
		result, err := service.New(service.Options{}).RenderDeploy(service.DeployOptions{TargetDir: args[0], Write: write, Overwrite: overwrite})
		for _, f := range result.Files {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", f.Status, f.Path)
			if !write {
				fmt.Fprintln(cmd.OutOrStdout(), string(f.Content))
			}
		}
		if result.Written {
			fmt.Fprintln(cmd.OutOrStdout(), "Wrote generated descriptors; project.yaml is unchanged. No service was deployed.")
		}
		return err
	}}
	child.Flags().BoolVar(&plan, "plan", false, "Preview generated files (the default)")
	child.Flags().BoolVar(&write, "write", false, "Write generated files and update their manifest digests")
	child.Flags().BoolVar(&overwrite, "overwrite", false, "Replace authored edits to generated files after reviewing the plan")
	parent.AddCommand(child)
	return parent
}
