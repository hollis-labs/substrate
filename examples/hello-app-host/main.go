package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run, err := boot.Start(ctx, substrateApp, substrateModules, substrateConfigJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := httphost.Serve(ctx, run.App, h, httphost.ServeOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
