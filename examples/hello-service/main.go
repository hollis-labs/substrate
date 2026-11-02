package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/hollis-labs/substrate/boot"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run, err := boot.Start(ctx, substrateApp, substrateModules, substrateConfigJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("started:", run.App.Started)
	if len(os.Args) > 1 && os.Args[1] == "-once" {
		_ = run.Stop(context.Background())
		return
	}
	<-ctx.Done()
	_ = run.Stop(context.Background())
}
