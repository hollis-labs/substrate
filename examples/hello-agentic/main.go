package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/mcp"
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

	// The app's only code: one MCP tool, plus its scope.
	mcp.Server(run.App).RegisterChecked(gmcp.Tool{
		Name: "hello", Description: "Say hello", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(context.Context, map[string]any) (any, error) { return map[string]any{"msg": "hello"}, nil },
	}, gmcp.Reads("returns a constant"))
	auth.ScopeTable(run.App).Tool("hello", substrateApp+".read")

	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	addr := "127.0.0.1:8080"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	if err := auth.ValidateBind(run.App, addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
