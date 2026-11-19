package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/genui"
)

var testScope = Scope{Namespace: "workspace", Actor: "actor"}

type catalogFunc func(context.Context, Scope, ShowRequest) (Surface, error)

func (f catalogFunc) Materialize(c context.Context, s Scope, q ShowRequest) (Surface, error) {
	return f(c, s, q)
}

type policyFunc func(context.Context, Request, func(context.Context, Decision) error) error

func (f policyFunc) WithFence(c context.Context, r Request, next func(context.Context, Decision) error) error {
	return f(c, r, next)
}

type executorFunc func(context.Context, Execution) (genui.Outcome, error)

func (f executorFunc) Execute(c context.Context, e Execution) (genui.Outcome, error) { return f(c, e) }

func fixtureCall() genui.Call {
	return genui.Call{Version: genui.ProtocolVersion, CallAgentFunction: genui.ActionRequest{SurfaceID: "surface_1", FunctionCallID: "invocation_1", CallFunction: genui.FunctionCall{Name: genui.FunctionName, CatalogID: genui.CatalogID, Args: genui.ActionArgs{Action: genui.Action{ID: "action_1", Subject: genui.Subject{Kind: "project", ID: "project_1"}, Basis: genui.Basis{Revision: "7", ProjectionRevision: "7"}, Risk: genui.RiskHigh}, Input: map[string]any{"choice": "allow"}}}}}
}
func fixtureSurface(t *testing.T) Surface {
	t.Helper()
	call := fixtureCall()
	deny := call.CallAgentFunction.CallFunction
	deny.Args.Input = map[string]any{"choice": "deny"}
	env, err := genui.NewSurface("surface_1", genui.PatternApproval, genui.ApprovalProps{Title: "Review", State: genui.ViewReady, Changes: []genui.Change{}, Choices: []genui.Choice{{Label: "Allow", CallFunction: call.CallAgentFunction.CallFunction}, {Label: "Deny", CallFunction: deny}}}, genui.DataModel{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := ChoiceDigest(call.CallAgentFunction.CallFunction)
	b, _ := ChoiceDigest(deny)
	return Surface{Scope: testScope, Envelope: env, Generation: "generation_1", ExpiresAt: time.Now().Add(time.Hour).UTC(), Operations: []OperationBinding{{a, "project.review"}, {b, "project.review"}}}
}

// allowPolicy is test-only authority. Production has no default grant policy.
func allowPolicy() Policy {
	return policyFunc(func(ctx context.Context, r Request, next func(context.Context, Decision) error) error {
		return next(ctx, Decision{Authority: &Authority{Generation: r.Surface.Generation, Deadline: r.Surface.ExpiresAt, Risk: genui.RiskHigh}})
	})
}
func serviceFixture(t *testing.T, store *fileStore, surface Surface, policy Policy, executor Executor) *Service {
	t.Helper()
	s, err := New(Ports{Catalog: catalogFunc(func(context.Context, Scope, ShowRequest) (Surface, error) { return surface, nil }), Surfaces: surfacePort{store}, Journal: store, Policy: policy, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	if err = (surfacePort{store}).Put(t.Context(), surface); err != nil {
		t.Fatal(err)
	}
	return s
}
func effectExecutor(store *fileStore, uncertain bool) Executor {
	return executorFunc(func(ctx context.Context, e Execution) (genui.Outcome, error) {
		if _, err := store.db.ExecContext(ctx, "INSERT INTO effects(action_id) VALUES(?)", e.Request.Call.CallAgentFunction.CallFunction.Args.Action.ID); err != nil {
			return genui.Outcome{}, err
		}
		if uncertain {
			return genui.Outcome{}, errors.New("private diagnostic must not leak")
		}
		return genui.Outcome{Status: genui.StatusOK, Result: json.RawMessage(`{"changed":true}`)}, nil
	})
}
func submit(t *testing.T, s *Service, call genui.Call) genui.Response {
	t.Helper()
	r, err := s.Submit(t.Context(), testScope, encode(call))
	if err != nil {
		t.Fatal(err)
	}
	if err = genui.ValidateJSON(genui.ResponseSchema, encode(r)); err != nil {
		t.Fatal("invalid emitted response", err)
	}
	return r
}
func status(r genui.Response) genui.Status {
	if r.AgentFunctionResponse.Value == nil {
		return ""
	}
	return r.AgentFunctionResponse.Value.Status
}

func TestConcurrentInstancesClaimOnceAndRecoverExactChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a := openFileStore(t, path)
	b := openFileStore(t, path)
	surface := fixtureSurface(t)
	s1 := serviceFixture(t, a, surface, allowPolicy(), effectExecutor(a, false))
	s2 := serviceFixture(t, b, surface, allowPolicy(), effectExecutor(b, false))
	call := fixtureCall()
	call.CallAgentFunction.CallFunction.Args.Input["choice"] = "deny"
	var wg sync.WaitGroup
	var winners atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := s1
			if i%2 == 0 {
				s = s2
			}
			r, _ := s.Submit(t.Context(), testScope, encode(call))
			if status(r) == genui.StatusOK {
				winners.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 || a.effectCount(t) != 1 {
		t.Fatalf("winners=%d effects=%d", winners.Load(), a.effectCount(t))
	}
	recovery, err := s2.Recover(t.Context(), testScope, "action_1", "reconnected_1")
	if err != nil {
		t.Fatal(err)
	}
	if status(recovery.Response) != genui.StatusOK || recovery.Request.CallAgentFunction.CallFunction.Args.Input["choice"] != "deny" || recovery.Request.CallAgentFunction.FunctionCallID != "reconnected_1" {
		t.Fatalf("wrong recovery: %+v", recovery)
	}
	if status(submit(t, s1, call)) != genui.StatusConflict {
		t.Fatal("terminal command was not conflict")
	}
	if _, err = s1.Recover(t.Context(), Scope{Namespace: testScope.Namespace, Actor: "other"}, "action_1", "invocation_2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross actor recovery", err)
	}
}

func TestUnknownEffectAndLostResultRemainConsumedAcrossReopen(t *testing.T) {
	for _, mode := range []string{"effect-error", "result-write", "claim-ack"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.db")
			db := openFileStore(t, path)
			surface := fixtureSurface(t)
			db.failResult.Store(mode == "result-write")
			db.loseClaimAck.Store(mode == "claim-ack")
			s := serviceFixture(t, db, surface, allowPolicy(), effectExecutor(db, mode == "effect-error"))
			r, err := s.Submit(t.Context(), testScope, encode(fixtureCall()))
			if !errors.Is(err, ErrUnavailable) || r.AgentFunctionResponse.Error == nil {
				t.Fatal("expected unknown", r, err)
			}
			before := db.effectCount(t)
			if mode == "claim-ack" && before != 0 {
				t.Fatal("effect after uncertain claim")
			}
			if mode != "claim-ack" && before != 1 {
				t.Fatal("effect not entered")
			}
			if err = db.close(); err != nil {
				t.Fatal(err)
			}
			reopened := openFileStore(t, path)
			s = serviceFixture(t, reopened, surface, allowPolicy(), effectExecutor(reopened, false))
			_, err = s.Submit(t.Context(), testScope, encode(fixtureCall()))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal("claim replayed", err)
			}
			if reopened.effectCount(t) != before {
				t.Fatal("replayed after restart")
			}
			recovered, err := s.Recover(t.Context(), testScope, "action_1", "read_1")
			if err != nil || recovered.Response.AgentFunctionResponse.Error == nil {
				t.Fatal("unknown became success", err)
			}
			rec, err := reopened.Load(t.Context(), Key{testScope, "action_1"})
			if err != nil || !rec.Claimed {
				t.Fatal("claim lost", err)
			}
		})
	}
}

func TestConfirmationSameIDProofExcludedButInputAndActorBound(t *testing.T) {
	db := newFileStore(t)
	surface := fixtureSurface(t)
	policy := policyFunc(func(ctx context.Context, r Request, next func(context.Context, Decision) error) error {
		proof := r.Call.CallAgentFunction.CallFunction.Args.Action.Confirmation
		if proof == "" {
			return next(ctx, Decision{Outcome: &genui.Outcome{Status: genui.StatusNeedsConfirmation, Confirm: &genui.Confirmation{What: "Approve review", Scope: []string{"project:project_1"}, TTLMS: 60000}}, Challenge: json.RawMessage(`{"fixture":"bound"}`)})
		}
		// Test-only proof: production policy must verify real actor/presence/grant.
		if proof != "fixture:"+r.Digest+":"+r.Scope.Actor || len(r.Previous.Challenge) == 0 {
			return next(ctx, Decision{Outcome: &genui.Outcome{Status: genui.StatusRefused, Reason: "denied"}})
		}
		return next(ctx, Decision{Authority: &Authority{Generation: r.Surface.Generation, Deadline: r.Surface.ExpiresAt, Risk: genui.RiskHigh}})
	})
	s := serviceFixture(t, db, surface, policy, effectExecutor(db, false))
	call := fixtureCall()
	if status(submit(t, s, call)) != genui.StatusNeedsConfirmation {
		t.Fatal("missing challenge")
	}
	rec, err := db.Load(t.Context(), Key{testScope, "action_1"})
	if err != nil || rec.Claimed {
		t.Fatal("challenge claimed", err)
	}
	changed := fixtureCall()
	changed.CallAgentFunction.CallFunction.Args.Input["choice"] = "deny"
	if status(submit(t, s, changed)) != genui.StatusConflict {
		t.Fatal("changed input coalesced")
	}
	call.CallAgentFunction.FunctionCallID = "confirmation_2"
	call.CallAgentFunction.CallFunction.CatalogID = "" // native optional catalog resolves to the fixed reviewed catalog
	call.CallAgentFunction.CallFunction.Args.Action.Confirmation = "fixture:" + rec.Digest + ":" + testScope.Actor
	if status(submit(t, s, call)) != genui.StatusOK || db.effectCount(t) != 1 {
		t.Fatal("confirmation retry failed")
	}
	recovery, err := s.Recover(t.Context(), testScope, "action_1", "read_3")
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Request.CallAgentFunction.CallFunction.Args.Action.Confirmation != "" || recovery.Request.CallAgentFunction.CallFunction.Args.Input["choice"] != "allow" {
		t.Fatal("proof persisted or choice lost")
	}
	var raw []byte
	if err = db.db.QueryRowContext(t.Context(), "SELECT record FROM actions").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || contains(raw, []byte(call.CallAgentFunction.CallFunction.Args.Action.Confirmation)) {
		t.Fatal("confirmation secret persisted")
	}
}
func contains(raw, part []byte) bool {
	for i := 0; i+len(part) <= len(raw); i++ {
		if string(raw[i:i+len(part)]) == string(part) {
			return true
		}
	}
	return false
}

func TestAuthorityIsFencedAndCannotBeSuppliedByClient(t *testing.T) {
	for _, mode := range []string{"revoked", "generation", "deadline", "double-callback", "risk-hint"} {
		t.Run(mode, func(t *testing.T) {
			db := newFileStore(t)
			surface := fixtureSurface(t)
			var executions atomic.Int32
			policy := policyFunc(func(ctx context.Context, r Request, next func(context.Context, Decision) error) error {
				if mode == "revoked" {
					return next(ctx, Decision{Outcome: &genui.Outcome{Status: genui.StatusRefused, Reason: "absent-scope"}})
				}
				a := Authority{Generation: r.Surface.Generation, Deadline: r.Surface.ExpiresAt, Risk: genui.RiskHigh}
				if mode == "generation" {
					a.Generation = "replacement"
				}
				if mode == "deadline" {
					a.Deadline = time.Now().Add(-time.Second)
				}
				err := next(ctx, Decision{Authority: &a})
				if mode == "double-callback" {
					_ = next(ctx, Decision{Authority: &a})
				}
				return err
			})
			s := serviceFixture(t, db, surface, policy, executorFunc(func(_ context.Context, e Execution) (genui.Outcome, error) {
				executions.Add(1)
				if e.Authority.Risk != genui.RiskHigh {
					t.Error("client risk trusted")
				}
				return genui.Outcome{Status: genui.StatusOK}, nil
			}))
			call := fixtureCall()
			call.CallAgentFunction.CallFunction.Args.Action.Risk = genui.RiskLow
			_, _ = s.Submit(t.Context(), testScope, encode(call))
			want := int32(0)
			if mode == "double-callback" || mode == "risk-hint" {
				want = 1
			}
			if executions.Load() != want {
				t.Fatalf("effects=%d", executions.Load())
			}
		})
	}
}

func TestPhysicalExecutorReceivesBoundedContextAndClaimSurvives(t *testing.T) {
	db := newFileStore(t)
	surface := fixtureSurface(t)
	var entered atomic.Bool
	policy := policyFunc(func(ctx context.Context, r Request, next func(context.Context, Decision) error) error {
		return next(ctx, Decision{Authority: &Authority{Generation: r.Surface.Generation, Deadline: time.Now().Add(30 * time.Millisecond), Risk: genui.RiskHigh}})
	})
	s := serviceFixture(t, db, surface, policy, executorFunc(func(ctx context.Context, _ Execution) (genui.Outcome, error) {
		entered.Store(true)
		<-ctx.Done()
		return genui.Outcome{}, ctx.Err()
	}))
	_, err := s.Submit(t.Context(), testScope, encode(fixtureCall()))
	if !errors.Is(err, ErrUnavailable) || !entered.Load() {
		t.Fatal("deadline not propagated")
	}
	r, err := db.Load(t.Context(), Key{testScope, "action_1"})
	if err != nil || !r.Claimed || r.Phase != PhaseUnknown {
		t.Fatal("expired effect claim lost")
	}
}

func TestAcceptedRecoveryNeverReentersExecutor(t *testing.T) {
	db := newFileStore(t)
	var calls atomic.Int32
	s := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), executorFunc(func(context.Context, Execution) (genui.Outcome, error) {
		calls.Add(1)
		return genui.Outcome{Status: genui.StatusAccepted, Handle: "pending_1"}, nil
	}))
	for i := 0; i < 2; i++ {
		if status(submit(t, s, fixtureCall())) != genui.StatusAccepted {
			t.Fatal("pending lost")
		}
	}
	r, err := s.Recover(t.Context(), testScope, "action_1", "read_2")
	if err != nil || status(r.Response) != genui.StatusAccepted || calls.Load() != 1 {
		t.Fatal("pending replayed", err)
	}
}

func TestFormPreservesCompleteTypedDraftAndRejectsInventedInput(t *testing.T) {
	db := newFileStore(t)
	surface := fixtureSurface(t)
	call := fixtureCall()
	call.CallAgentFunction.CallFunction.Args.Input = map[string]any{"review": "form"}
	p := genui.FormProps{Title: "Edit", State: genui.ViewReady, Fields: []genui.FormField{{Key: "name", Label: "Name", Type: "text", Required: true}}, Submit: genui.Choice{Label: "Review", CallFunction: call.CallAgentFunction.CallFunction}}
	var err error
	surface.Envelope, err = genui.NewSurface("surface_1", genui.PatternForm, p, genui.DataModel{})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := ChoiceDigest(p.Submit.CallFunction)
	surface.Operations = []OperationBinding{{digest, "project.prepare_review"}}
	s := serviceFixture(t, db, surface, allowPolicy(), effectExecutor(db, false))
	call.CallAgentFunction.CallFunction.Args.Input["draft"] = map[string]any{"invented": "bad"}
	if _, err = s.Submit(t.Context(), testScope, encode(call)); !errors.Is(err, ErrInvalid) {
		t.Fatal("invented draft accepted", err)
	}
	call.CallAgentFunction.CallFunction.Args.Input["draft"] = map[string]any{"name": "reviewed value"}
	if status(submit(t, s, call)) != genui.StatusOK {
		t.Fatal("complete form refused")
	}
	recovery, err := s.Recover(t.Context(), testScope, "action_1", "read_1")
	if err != nil {
		t.Fatal(err)
	}
	draft := recovery.Request.CallAgentFunction.CallFunction.Args.Input["draft"].(map[string]any)
	if draft["name"] != "reviewed value" {
		t.Fatal("draft lost")
	}
}

func TestCorruptRetainedRecordNeverReopensAction(t *testing.T) {
	for _, mutation := range []string{"phase", "claimed", "request", "proof"} {
		t.Run(mutation, func(t *testing.T) {
			db := newFileStore(t)
			s := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), effectExecutor(db, false))
			submit(t, s, fixtureCall())
			key := Key{testScope, "action_1"}
			r, err := db.Load(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "phase":
				r.Phase = "future"
			case "claimed":
				r.Claimed = false
			case "request":
				r.Request.CallAgentFunction.CallFunction.Args.Action.ID = "different"
			case "proof":
				r.Request.CallAgentFunction.CallFunction.Args.Action.Confirmation = "forbidden-secret"
			}
			if _, err = db.db.ExecContext(t.Context(), "UPDATE actions SET record=? WHERE key=?", encode(r), dbKey(key)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Submit(t.Context(), testScope, encode(fixtureCall())); !errors.Is(err, ErrUnavailable) {
				t.Fatal("corrupt record accepted", err)
			}
			if _, err = s.Recover(t.Context(), testScope, "action_1", "read_1"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("corrupt record recovered", err)
			}
			if db.effectCount(t) != 1 {
				t.Fatal("corruption retried effect")
			}
		})
	}
}

func TestCallerCancellationCannotBeDetachedByPolicy(t *testing.T) {
	db := newFileStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	policy := policyFunc(func(c context.Context, r Request, next func(context.Context, Decision) error) error {
		cancel()
		return next(context.WithoutCancel(c), Decision{Authority: &Authority{Generation: r.Surface.Generation, Deadline: r.Surface.ExpiresAt, Risk: genui.RiskHigh}})
	})
	s := serviceFixture(t, db, fixtureSurface(t), policy, executorFunc(func(context.Context, Execution) (genui.Outcome, error) {
		calls.Add(1)
		return genui.Outcome{Status: genui.StatusOK}, nil
	}))
	if _, err := s.Submit(ctx, testScope, encode(fixtureCall())); !errors.Is(err, ErrUnavailable) {
		t.Fatal("detached caller accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatal("effect after caller cancellation")
	}
}

func TestPolicyCannotEnterAfterReturningFence(t *testing.T) {
	db := newFileStore(t)
	release := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	policy := policyFunc(func(ctx context.Context, r Request, next func(context.Context, Decision) error) error {
		go func() {
			defer close(done)
			<-release
			_ = next(context.WithoutCancel(ctx), Decision{Authority: &Authority{Generation: r.Surface.Generation, Deadline: r.Surface.ExpiresAt, Risk: genui.RiskHigh}})
		}()
		return nil
	})
	s := serviceFixture(t, db, fixtureSurface(t), policy, executorFunc(func(context.Context, Execution) (genui.Outcome, error) {
		calls.Add(1)
		return genui.Outcome{Status: genui.StatusOK}, nil
	}))
	if _, err := s.Submit(t.Context(), testScope, encode(fixtureCall())); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing synchronous policy accepted", err)
	}
	close(release)
	<-done
	if calls.Load() != 0 {
		t.Fatal("delayed callback executed after fence return")
	}
}

func TestSemanticIntentBindsAuthenticatedScope(t *testing.T) {
	surface := fixtureSurface(t)
	call := fixtureCall()
	a, err := intentDigest(surface, call, "project.review")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{{Namespace: "other", Actor: testScope.Actor}, {Namespace: testScope.Namespace, Actor: "other"}} {
		surface.Scope = scope
		b, err := intentDigest(surface, call, "project.review")
		if err != nil || a == b {
			t.Fatal("scope missing from semantic digest", err)
		}
	}
}

func TestCompleteResponseLimitNeverEmitsOversizedSuccessOrReexecutes(t *testing.T) {
	parts := map[string]string{"a": strings.Repeat("a", 16000), "b": strings.Repeat("b", 16000), "c": strings.Repeat("c", 16000), "d": strings.Repeat("d", 16000), "tail": ""}
	o := genui.Outcome{Status: genui.StatusOK, Result: encode(parts)}
	parts["tail"] = strings.Repeat("z", 65500-len(encode(o)))
	o.Result = encode(parts)
	if _, err := validOutcome(o); err != nil {
		t.Fatal("boundary outcome must itself be valid", err)
	}
	if len(encode(genui.Response{Version: genui.ProtocolVersion, AgentFunctionResponse: genui.FunctionResponse{FunctionCallID: "invocation_1", Value: &o}})) <= genui.MaxActionBytes {
		t.Fatal("fixture did not cross full response boundary")
	}
	db := newFileStore(t)
	var calls atomic.Int32
	s := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), executorFunc(func(context.Context, Execution) (genui.Outcome, error) { calls.Add(1); return o, nil }))
	r := submit(t, s, fixtureCall())
	if r.AgentFunctionResponse.Error == nil || calls.Load() != 1 {
		t.Fatal("oversized success emitted")
	}
	recovered, err := s.Recover(t.Context(), testScope, "action_1", "read_1")
	if err != nil || recovered.Response.AgentFunctionResponse.Error == nil {
		t.Fatal("oversized recovery emitted", err)
	}
	duplicate := submit(t, s, fixtureCall())
	if duplicate.AgentFunctionResponse.Error == nil || calls.Load() != 1 {
		t.Fatal("large recorded result enabled retry")
	}
	record, err := db.Load(t.Context(), Key{testScope, "action_1"})
	if err != nil || !record.Claimed || record.Phase != PhaseTerminal {
		t.Fatal("known durable result was erased", err)
	}
}
