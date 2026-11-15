// Command hello is a minimal subprocess plugin used by the pluginhost tests.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

type hello struct{}

// Init answers with the id the host passed as config "id" (default "hello"),
// so one binary can stand in for several plugins.
func (hello) Init(_ context.Context, p subprocess.InitParams) (subprocess.InitResult, error) {
	id := p.Config["id"]
	if id == "" {
		id = "hello"
	}
	return subprocess.InitResult{ID: id, Name: "Hello", Version: "0.1.0", Description: "test plugin",
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
