package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/genui"
)

func TestUnicodeActionIDExecutesAndRecoversAtContractBoundary(t *testing.T) {
	for _, test := range []struct{ name, id string }{
		{"multibyte129", strings.Repeat("é", 129)},
		{"multibyte256", strings.Repeat("é", 256)},
		{"emoji256", strings.Repeat("🌿", 256)},
		{"literal-replacement256", strings.Repeat("�", 256)},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := test.id
			db := newFileStore(t)
			surface, call := fixtureSurface(t), fixtureCall()
			call.CallAgentFunction.CallFunction.Args.Action.ID = id
			p := genui.ApprovalProps{Title: "Review", State: genui.ViewReady, Changes: []genui.Change{}, Choices: []genui.Choice{{Label: "Allow", CallFunction: call.CallAgentFunction.CallFunction}}}
			var err error
			surface.Envelope, err = genui.NewSurface("surface_1", genui.PatternApproval, p, genui.DataModel{})
			if err != nil {
				t.Fatal(err)
			}
			digest, err := ChoiceDigest(call.CallAgentFunction.CallFunction)
			if err != nil {
				t.Fatal(err)
			}
			surface.Operations = []OperationBinding{{digest, "project.review"}}
			s := serviceFixture(t, db, surface, allowPolicy(), effectExecutor(db, false))
			if status(submit(t, s, call)) != genui.StatusOK {
				t.Fatal("valid Unicode ID refused")
			}
			invocation := strings.Repeat("é", 256)
			recovered, err := s.Recover(t.Context(), testScope, id, invocation)
			if err != nil || status(recovered.Response) != genui.StatusOK {
				t.Fatal("executed ID cannot recover", err)
			}
			if recovered.Request.CallAgentFunction.CallFunction.Args.Action.ID != id || recovered.Response.AgentFunctionResponse.FunctionCallID != invocation {
				t.Fatal("Unicode identity changed")
			}
			if db.effectCount(t) != 1 {
				t.Fatal("recovery repeated effect")
			}
			for _, invalid := range []string{strings.Repeat("é", 257), string([]byte{0xff}), ""} {
				if _, err := s.Recover(t.Context(), testScope, invalid, "read"); !errors.Is(err, ErrInvalid) {
					t.Fatal("invalid action ID accepted", err)
				}
				if _, err := s.Recover(t.Context(), testScope, id, invalid); !errors.Is(err, ErrInvalid) {
					t.Fatal("invalid invocation ID accepted", err)
				}
			}
		})
	}
}

func TestBothShowAdaptersRequireExactKeysBeforeHostMaterialization(t *testing.T) {
	db := newFileStore(t)
	surface := fixtureSurface(t)
	s := serviceFixture(t, db, surface, allowPolicy(), effectExecutor(db, false))
	var materialized atomic.Int32
	s.ports.Catalog = catalogFunc(func(context.Context, Scope, ShowRequest) (Surface, error) { materialized.Add(1); return surface, nil })
	resolver := resolverFunc(func(context.Context, libauth.Principal) (Scope, error) { return testScope, nil })
	tool, err := NewShowPatternTool(s, resolver, "sample.genui.show")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(s, resolver, "sample.genui")
	if err != nil {
		t.Fatal(err)
	}
	verifier := libauth.NewAPIKeyVerifier(libauth.APIKey{ID: "fixture", Hash: libauth.HashKey("fixture-show-boundary"), Scopes: []string{"sample.genui.show"}})
	p, err := verifier.Verify(t.Context(), "fixture-show-boundary")
	if err != nil {
		t.Fatal(err)
	}
	ctx := libauth.WithPrincipal(t.Context(), p)
	for _, raw := range []string{
		`{"Pattern":"approval","params":{}}`,
		`{"pattern":"approval","PARAMS":{}}`,
		`{"pattern":"approval","Pattern":"form","params":{}}`,
		`{"pattern":"approval","params":{},"Params":{}}`,
		`{"pattern":"approval","params":{},"unknown":1}`,
		`{"pattern":"approval","params":null}`,
		`{"pattern":"approval","params":[]}`,
		`{"pattern":"approval"}`,
		`{"params":{}}`,
		`{"pattern":null,"params":{}}`,
		`{"pattern":"approval","pattern":"form","params":{}}`,
	} {
		if _, err = tool.Handle(ctx, json.RawMessage(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("tool accepted schema-invalid request %s: %v", raw, err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/genui/surfaces", bytes.NewBufferString(raw)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.Handlers()["POST /api/genui/surfaces"].ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("HTTP accepted schema-invalid request %s: %d", raw, w.Code)
		}
	}
	if materialized.Load() != 0 {
		t.Fatal("invalid request reached host materializer")
	}
	valid := json.RawMessage(`{"pattern":"approval","params":{"title":"Valid Unicode 🌿"}}`)
	if _, err = tool.Handle(ctx, valid); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/genui/surfaces", bytes.NewReader(valid)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Handlers()["POST /api/genui/surfaces"].ServeHTTP(w, r)
	if w.Code != 200 || materialized.Load() != 2 || db.effectCount(t) != 0 {
		t.Fatal("valid show path changed")
	}
}

func TestGatewayAppliesSharedTypedPreflightBeforeEncoding(t *testing.T) {
	db := newFileStore(t)
	surface := fixtureSurface(t)
	surface.Envelope.Title = string([]byte{0xff})
	if _, err := validateSurface(surface, testScope); !errors.Is(err, ErrUnavailable) {
		t.Fatal("malformed typed surface replaced", err)
	}
	s := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), executorFunc(func(context.Context, Execution) (genui.Outcome, error) {
		return genui.Outcome{Status: genui.StatusFailed, Code: "test", Message: string([]byte{0xff})}, nil
	}))
	if r, err := s.Submit(t.Context(), testScope, encode(fixtureCall())); !errors.Is(err, ErrUnavailable) || r.AgentFunctionResponse.Error == nil {
		t.Fatal("malformed typed outcome replaced", err)
	}
	rec, err := db.Load(t.Context(), Key{testScope, "action_1"})
	if err != nil || !rec.Claimed || rec.Phase != PhaseUnknown {
		t.Fatal("malformed result released claim", err)
	}
}
