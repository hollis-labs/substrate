package conformance

import (
	"context"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
	"github.com/hollis-labs/substrate/module"
	genuiModule "github.com/hollis-labs/substrate/modules/genui"
)

// Lifecycle conformance supplies explicit host ports, just as it supplies the
// policy and router hooks. These ports refuse operations: starting a module
// must not invent application storage or grant authority to test requests.
func conformanceGenuiHost(*module.App) (genuiModule.Host, error) {
	p := conformanceGenuiPorts{}
	return genuiModule.Host{
		Ports:    gateway.Ports{Catalog: p, Surfaces: p, Journal: p, Policy: p, Executor: p},
		Resolver: p,
	}, nil
}

type conformanceGenuiPorts struct{}

func (conformanceGenuiPorts) Materialize(context.Context, gateway.Scope, gateway.ShowRequest) (gateway.Surface, error) {
	return gateway.Surface{}, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Put(context.Context, gateway.Surface) error {
	return gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Get(context.Context, gateway.Scope, string) (gateway.Surface, error) {
	return gateway.Surface{}, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Supersede(context.Context, gateway.Scope, string, string) error {
	return gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Begin(context.Context, gateway.Key, gateway.Record) (gateway.Record, bool, error) {
	return gateway.Record{}, false, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Load(context.Context, gateway.Key) (gateway.Record, error) {
	return gateway.Record{}, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) CompareAndSwap(context.Context, gateway.Key, uint64, gateway.Record) (gateway.Record, bool, error) {
	return gateway.Record{}, false, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) WithFence(context.Context, gateway.Request, func(context.Context, gateway.Decision) error) error {
	return gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Execute(context.Context, gateway.Execution) (genui.Outcome, error) {
	return genui.Outcome{}, gateway.ErrUnavailable
}
func (conformanceGenuiPorts) Resolve(context.Context, libauth.Principal) (gateway.Scope, error) {
	return gateway.Scope{}, gateway.ErrForbidden
}
