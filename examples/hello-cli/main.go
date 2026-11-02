package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
)

func main() {
	ctx := context.Background()
	run, err := boot.Start(ctx, substrateApp, substrateModules, substrateConfigJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := cli.Root(run.App)
	// The app's own commands go here; Substrate supplies version, config, health.
	root.AddCommand(&cobra.Command{
		Use: "hello", Short: "Say hello",
		Run: func(c *cobra.Command, _ []string) { _, _ = fmt.Fprintln(c.OutOrStdout(), "hello") },
	})
	err = root.ExecuteContext(ctx)
	_ = run.Stop(ctx)
	if err != nil {
		os.Exit(1)
	}
}
