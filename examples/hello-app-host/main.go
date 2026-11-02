package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	addr := "127.0.0.1:8080"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	ln, err := net.Listen("tcp", addr) // the app owns the listener
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: h}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	_ = srv.Serve(ln)
}
