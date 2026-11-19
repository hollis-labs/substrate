package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
)

// dbCommand is the `<app> db` group, added to the app's root when the cli module
// is selected and the app has named stores:
//
//	<app> db migrate --store <name>
//
// migrate applies the migrations registered for the named store over its
// migration credential (migrate_dsn_file or migrate_dsn_env), whatever the
// store's schema_mode says. It is the deploy step for a Postgres named store,
// which defaults to schema_mode verify and so never migrates from the running
// app. It boots the app's modules like the server does and exits when done.
func dbCommand(app *module.App) *cobra.Command {
	db := &cobra.Command{Use: "db", Short: "Database operations"}
	var storeName string
	migrate := &cobra.Command{
		Use:   "migrate --store <name>",
		Short: "Apply a named store's migrations (a deploy step; uses migrate_dsn_*)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if storeName == "" {
				return errors.New("--store is required: the name under database.stores")
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if err := MigrateNamed(ctx, app, storeName); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "store %s: migrations applied\n", storeName)
			return nil
		},
	}
	migrate.Flags().StringVar(&storeName, "store", "", "named store to migrate (a key under database.stores)")
	db.AddCommand(migrate)
	return db
}
