// Command hello is a minimal subprocess plugin used by the pluginhost tests.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

type hello struct{}

func (hello) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{ID: "hello", Name: "Hello", Version: "0.1.0", Description: "test plugin",
		Protocol: subprocess.ProtocolVersion, CapabilityContract: 1}, nil
}
func (hello) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (hello) Unload(context.Context) error { return nil }
func (hello) Command(_ context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	return subprocess.CommandResult{Action: "message", Content: "hello, " + req.Args}, nil
}

func main() {
	if err := subprocess.Serve(hello{}); err != nil {
		os.Exit(1)
	}
}
