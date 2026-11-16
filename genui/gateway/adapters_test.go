package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes"
	"github.com/hollis-labs/substrate/genui"
)

func TestShowToolUsesVerifiedHostScopeAndRawReviewedParameters(t *testing.T) {
	db := newFileStore(t)
	s := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), effectExecutor(db, false))
	tool, err := NewShowPatternTool(s, resolverFunc(func(context.Context, libauth.Principal) (Scope, error) { return testScope, nil }), "sample.genui.show")
	if err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"pattern":"approval","params":{"title":"Review"}}`)
	if _, err = tool.Handle(t.Context(), valid); !errors.Is(err, ErrForbidden) {
		t.Fatal("unauthenticated tool accepted")
	}
	verifier := libauth.NewAPIKeyVerifier(libauth.APIKey{ID: "agent", Hash: libauth.HashKey("fixture-show"), Scopes: []string{"sample.genui.show"}})
	p, err := verifier.Verify(t.Context(), "fixture-show")
	if err != nil {
		t.Fatal(err)
	}
	ctx := libauth.WithPrincipal(t.Context(), p)
	if _, err = tool.Handle(ctx, valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"pattern":"approval","params":{},"url":"https://example.invalid"}`,
		`{"pattern":"approval","pattern":"form","params":{}}`,
		`{"pattern":"approval","params":{"amount":1.0000000000000000001}}`,
		`{"pattern":"approval","params":{"amount":9007199254740993}}`,
		`{"pattern":"arbitrary","params":{}}`,
	} {
		if _, err = tool.Handle(ctx, json.RawMessage(raw)); err == nil {
			t.Fatalf("unreviewed tool input accepted: %s", raw)
		}
	}
	if db.effectCount(t) != 0 {
		t.Fatal("show tool executed action")
	}
}

func TestEnvelopeRegistrationUsesExistingNamespacedExtension(t *testing.T) {
	r := envelopes.NewRegistry()
	if err := RegisterEnvelope(r); err != nil {
		t.Fatal(err)
	}
	if err := RegisterEnvelope(r); !errors.Is(err, envelopes.ErrConflict) {
		t.Fatal("registration silently replaced", err)
	}
	raw := encode(fixtureSurface(t).Envelope)
	var e envelopes.Envelope
	if err := decode(raw, &e); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateEnvelope(&e); err != nil {
		t.Fatal(err)
	}
	var invalid map[string]any
	if err := decode(raw, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid["data"].(map[string]any)["createSurface"].(map[string]any)["components"] = []any{}
	if err := decode(encode(invalid), &e); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateEnvelope(&e); err == nil {
		t.Fatal("incomplete root accepted")
	}
	if _, err := genui.DecodeEnvelope(encode(invalid)); err == nil {
		t.Fatal("profile validator accepted incomplete root")
	}
}
