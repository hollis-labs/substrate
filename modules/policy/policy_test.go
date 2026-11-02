package policy_test

import (
	"context"
	"testing"

	libpolicy "github.com/hollis-labs/libs/security/policy"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/policy"
)

type denyAll struct{}

func (denyAll) Evaluate(context.Context, libpolicy.Request) (libpolicy.Decision, error) {
	return libpolicy.Decision{Effect: libpolicy.Deny, Reason: "test"}, nil
}

var req = libpolicy.Request{
	Principal: libpolicy.Principal{URN: "urn:p:1", Kind: "user"},
	Action:    "notes.write", Resource: libpolicy.Resource{Type: "note", ID: "1"}, Scope: "t",
}

func start(t *testing.T, cfg string) *policy.Authorizer {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"policy"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return policy.From(run.App)
}

func TestNoPDPFailsStart(t *testing.T) {
	policy.UsePDP(nil)
	if _, err := boot.Start(context.Background(), "t", []string{"policy"}, ""); err == nil {
		t.Fatal("starting without a PDP must fail, not silently allow")
	}
}

func TestShadowObservesEnforceBlocks(t *testing.T) {
	policy.UsePDP(denyAll{})
	defer policy.UsePDP(nil)

	r, err := start(t, "").Evaluate(context.Background(), req) // default: shadow
	if err != nil || r.Admission != libpolicy.Allow || !r.WouldBlock {
		t.Fatalf("shadow: %+v err=%v", r, err)
	}
	r, err = start(t, `{"policy":{"mode":"enforce"}}`).Evaluate(context.Background(), req)
	if err != nil || r.Admission != libpolicy.Deny {
		t.Fatalf("enforce: %+v err=%v", r, err)
	}
}

func TestAllowAllIsExplicit(t *testing.T) {
	policy.UsePDP(policy.AllowAll{})
	defer policy.UsePDP(nil)
	r, err := start(t, `{"policy":{"mode":"enforce"}}`).Evaluate(context.Background(), req)
	if err != nil || r.Admission != libpolicy.Allow {
		t.Fatalf("%+v err=%v", r, err)
	}
}

func TestBadModeFailsStart(t *testing.T) {
	policy.UsePDP(denyAll{})
	defer policy.UsePDP(nil)
	if _, err := boot.Start(context.Background(), "t", []string{"policy"}, `{"policy":{"mode":"bogus"}}`); err == nil {
		t.Fatal("expected error")
	}
}
