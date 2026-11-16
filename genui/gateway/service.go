package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/substrate/genui"
)

type Service struct{ ports Ports }

func New(p Ports) (*Service, error) {
	if p.Catalog == nil || p.Surfaces == nil || p.Journal == nil || p.Policy == nil || p.Executor == nil {
		return nil, fmt.Errorf("%w: all host ports are required", ErrUnavailable)
	}
	return &Service{ports: p}, nil
}

func validScope(s Scope) bool {
	return s.Namespace != "" && s.Actor != "" && strings.TrimSpace(s.Namespace) == s.Namespace && strings.TrimSpace(s.Actor) == s.Actor && len(s.Namespace) <= 256 && len(s.Actor) <= 256
}

func hash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	b, err = genui.CanonicalJSON(b)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// ChoiceDigest identifies a published offered choice independently of an
// advisory risk hint and confirmation proof. Hosts bind this to an operation.
func ChoiceDigest(c genui.FunctionCall) (string, error) {
	c.CatalogID = genui.CatalogID
	c.Args.Action.Confirmation = ""
	c.Args.Action.Risk = ""
	return hash(c)
}

func (s *Service) Show(ctx context.Context, scope Scope, q ShowRequest) (genui.Envelope, error) {
	if !validScope(scope) {
		return genui.Envelope{}, ErrForbidden
	}
	if q.Pattern != genui.PatternApproval && q.Pattern != genui.PatternForm && q.Pattern != genui.PatternListDetail {
		return genui.Envelope{}, ErrInvalid
	}
	if len(q.Params) > genui.MaxActionBytes {
		return genui.Envelope{}, ErrInvalid
	}
	canonical, err := genui.CanonicalJSON(q.Params)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return genui.Envelope{}, ErrInvalid
	}
	q.Params = canonical
	surface, err := s.ports.Catalog.Materialize(ctx, scope, q)
	if err != nil {
		return genui.Envelope{}, ErrUnavailable
	}
	surface, err = validateSurface(surface, scope)
	if err != nil {
		return genui.Envelope{}, err
	}
	if surface.Envelope.Data.CreateSurface.Metadata.Extensions.Pattern.ID != q.Pattern {
		return genui.Envelope{}, ErrUnavailable
	}
	if surface.Superseded || !time.Now().Before(surface.ExpiresAt) {
		return genui.Envelope{}, ErrUnavailable
	}
	if err = s.ports.Surfaces.Put(ctx, surface); err != nil {
		return genui.Envelope{}, ErrUnavailable
	}
	return surface.Envelope, nil
}

func validateSurface(v Surface, scope Scope) (Surface, error) {
	if v.Scope != scope || v.Generation == "" || len(v.Generation) > 256 || v.ExpiresAt.IsZero() {
		return Surface{}, ErrUnavailable
	}
	// Apply the shared typed preflight before encoding can replace malformed
	// host-provided strings or map keys.
	if _, err := genui.Choices(v.Envelope); err != nil {
		return Surface{}, ErrUnavailable
	}
	b, err := json.Marshal(v.Envelope)
	if err != nil {
		return Surface{}, ErrUnavailable
	}
	v.Envelope, err = genui.DecodeEnvelope(b)
	if err != nil {
		return Surface{}, ErrUnavailable
	}
	choices, err := genui.Choices(v.Envelope)
	if err != nil {
		return Surface{}, ErrUnavailable
	}
	offered := map[string]bool{}
	for _, choice := range choices {
		d, e := ChoiceDigest(choice.CallFunction)
		if e != nil {
			return Surface{}, ErrUnavailable
		}
		offered[d] = true
	}
	seen := map[string]bool{}
	for _, binding := range v.Operations {
		if !offered[binding.ChoiceDigest] || seen[binding.ChoiceDigest] || binding.Operation == "" || len(binding.Operation) > 256 {
			return Surface{}, ErrUnavailable
		}
		seen[binding.ChoiceDigest] = true
	}
	if len(seen) != len(offered) {
		return Surface{}, ErrUnavailable
	}
	return v, nil
}

func match(v Surface, call genui.Call) (string, error) {
	choices, err := genui.Choices(v.Envelope)
	if err != nil {
		return "", ErrUnavailable
	}
	candidate := call.CallAgentFunction.CallFunction
	if v.Envelope.Data.CreateSurface.Metadata.Extensions.Pattern.ID == genui.PatternForm {
		var p genui.FormProps
		if err = json.Unmarshal(v.Envelope.Data.CreateSurface.Components[0].Props, &p); err != nil {
			return "", ErrUnavailable
		}
		draft, ok := candidate.Args.Input["draft"].(map[string]any)
		if !ok || genui.ValidateFormDraft(p, draft, true) != nil {
			return "", ErrInvalid
		}
		input := make(map[string]any, len(candidate.Args.Input))
		for k, value := range candidate.Args.Input {
			if k != "draft" {
				input[k] = value
			}
		}
		candidate.Args.Input = input
	}
	digest, err := ChoiceDigest(candidate)
	if err != nil {
		return "", ErrInvalid
	}
	for _, choice := range choices {
		want, e := ChoiceDigest(choice.CallFunction)
		if e != nil {
			return "", ErrUnavailable
		}
		if want == digest {
			for _, binding := range v.Operations {
				if binding.ChoiceDigest == want {
					return binding.Operation, nil
				}
			}
		}
	}
	return "", ErrForbidden
}

func intentDigest(surface Surface, call genui.Call, operation string) (string, error) {
	call.CallAgentFunction.FunctionCallID = ""
	call.CallAgentFunction.CallFunction.Args.Action.Confirmation = ""
	call.CallAgentFunction.CallFunction.Args.Action.Risk = ""
	call.CallAgentFunction.CallFunction.CatalogID = genui.CatalogID
	return hash(struct {
		Scope      Scope
		Surface    genui.Envelope
		Generation string
		ExpiresAt  string
		Operation  string
		Call       genui.Call
	}{surface.Scope, surface.Envelope, surface.Generation, surface.ExpiresAt.UTC().Format(time.RFC3339Nano), operation, call})
}

func response(id string, outcome genui.Outcome) genui.Response {
	r := genui.Response{Version: genui.ProtocolVersion, AgentFunctionResponse: genui.FunctionResponse{FunctionCallID: id, Value: &outcome}}
	// An individually bounded outcome may exceed the complete RPC limit after
	// correlation/wrapping (or when nested as a recorded conflict). Never emit
	// an unreadable success; the journal remains authoritative and consumed.
	raw, err := json.Marshal(r)
	if err != nil || genui.ValidateJSON(genui.ResponseSchema, raw) != nil {
		return unavailable(id)
	}
	return r
}
func unavailable(id string) genui.Response {
	return genui.Response{Version: genui.ProtocolVersion, AgentFunctionResponse: genui.FunctionResponse{FunctionCallID: id, Error: &genui.ProtocolError{Code: "unavailable", Message: "Action result is unavailable; do not resubmit."}}}
}
func conflict(id string, r Record) genui.Response {
	if r.Outcome == nil {
		return genui.Response{Version: genui.ProtocolVersion, AgentFunctionResponse: genui.FunctionResponse{FunctionCallID: id, Error: &genui.ProtocolError{Code: "conflict", Message: "Action ID is already bound; recover its result without resubmitting."}}}
	}
	o := genui.Outcome{Status: genui.StatusConflict, Recorded: r.Outcome}
	return response(id, o)
}
func validOutcome(o genui.Outcome) (genui.Outcome, error) {
	if err := genui.ValidateOutcome(o); err != nil {
		return genui.Outcome{}, err
	}
	b, err := json.Marshal(o)
	if err != nil {
		return genui.Outcome{}, err
	}
	return genui.DecodeOutcome(b)
}

// Refuse corrupt/incoherent retained records rather than treating them as a
// fresh action. The storage adapter still owns atomicity and durable commit.
func validRecord(key Key, r Record) bool {
	if r.Revision == 0 || len(r.Digest) != 64 || r.Request.CallAgentFunction.CallFunction.Args.Action.ID != key.ActionID || r.Request.CallAgentFunction.CallFunction.Args.Action.Confirmation != "" {
		return false
	}
	if _, err := hex.DecodeString(r.Digest); err != nil {
		return false
	}
	b, err := json.Marshal(r.Request)
	if err != nil {
		return false
	}
	if _, err = genui.DecodeCall(b); err != nil {
		return false
	}
	if r.Outcome != nil {
		if _, err = validOutcome(*r.Outcome); err != nil {
			return false
		}
	}
	switch r.Phase {
	case PhaseReady:
		return !r.Claimed && r.Outcome == nil && len(r.Challenge) == 0
	case PhaseWaiting:
		return !r.Claimed && r.Outcome != nil && (r.Outcome.Status == genui.StatusStale || (r.Outcome.Status == genui.StatusNeedsConfirmation && len(r.Challenge) > 0))
	case PhaseClaimed, PhaseUnknown:
		return r.Claimed && r.Outcome == nil && len(r.Challenge) == 0
	case PhasePending:
		return r.Claimed && r.Outcome != nil && r.Outcome.Status == genui.StatusAccepted && len(r.Challenge) == 0
	case PhaseTerminal:
		return r.Outcome != nil && r.Outcome.Status.Terminal() && len(r.Challenge) == 0 && (r.Claimed || (r.Outcome.Status != genui.StatusOK && r.Outcome.Status != genui.StatusFailed))
	}
	return false
}

// Submit accepts only the canonical contract. Identity and trusted runtime
// facts are supplied separately by the host, never decoded from action JSON.
func (s *Service) Submit(ctx context.Context, scope Scope, raw []byte) (genui.Response, error) {
	if !validScope(scope) {
		return genui.Response{}, ErrForbidden
	}
	call, err := genui.DecodeCall(raw)
	if err != nil {
		return genui.Response{}, ErrInvalid
	}
	id := call.CallAgentFunction.FunctionCallID
	key := Key{scope, call.CallAgentFunction.CallFunction.Args.Action.ID}
	previous, loadErr := s.ports.Journal.Load(ctx, key)
	if loadErr != nil && !errors.Is(loadErr, ErrNotFound) {
		return unavailable(id), ErrUnavailable
	}
	if loadErr == nil && !validRecord(key, previous) {
		return unavailable(id), ErrUnavailable
	}
	if loadErr == nil && previous.Phase == PhaseTerminal {
		return conflict(id, previous), nil
	}
	v, err := s.ports.Surfaces.Get(ctx, scope, call.CallAgentFunction.SurfaceID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return unavailable(id), ErrUnavailable
		}
		if loadErr == nil {
			return conflict(id, previous), nil
		}
		return genui.Response{}, ErrNotFound
	}
	v, err = validateSurface(v, scope)
	if err != nil {
		return unavailable(id), err
	}
	op, err := match(v, call)
	if err != nil {
		if loadErr == nil {
			return conflict(id, previous), nil
		}
		return genui.Response{}, err
	}
	digest, err := intentDigest(v, call, op)
	if err != nil {
		return genui.Response{}, ErrInvalid
	}
	stored := call
	stored.CallAgentFunction.CallFunction.Args.Action.Confirmation = ""
	rec, _, err := s.ports.Journal.Begin(ctx, key, Record{Revision: 1, Digest: digest, Request: stored, Phase: PhaseReady})
	if err != nil {
		return unavailable(id), ErrUnavailable
	}
	if !validRecord(key, rec) {
		return unavailable(id), ErrUnavailable
	}
	if rec.Digest != digest {
		return conflict(id, rec), nil
	}
	if rec.Phase == PhaseTerminal {
		return conflict(id, rec), nil
	}
	if rec.Claimed || rec.Phase == PhaseUnknown {
		if rec.Phase == PhasePending && rec.Outcome != nil {
			return response(id, *rec.Outcome), nil
		}
		return unavailable(id), ErrUnavailable
	}
	if v.Superseded {
		return s.saveNonexecution(ctx, key, rec, id, genui.Outcome{Status: genui.StatusSuperseded}, nil)
	}
	if !time.Now().Before(v.ExpiresAt) {
		return s.saveNonexecution(ctx, key, rec, id, genui.Outcome{Status: genui.StatusExpired}, nil)
	}
	req := Request{Scope: scope, Surface: v, Call: call, Operation: op, Digest: digest, Previous: rec}
	// Close admission when the policy call returns. A delayed callback cannot
	// execute after the host has released its synchronous fence.
	var visitState atomic.Uint32
	finished := make(chan struct{})
	policyCtx, cancelPolicy := context.WithCancel(ctx)
	defer cancelPolicy()
	var result genui.Response
	var callbackErr error
	policyErr := s.ports.Policy.WithFence(policyCtx, req, func(fenced context.Context, d Decision) error {
		if !visitState.CompareAndSwap(0, 1) {
			return ErrUnavailable
		}
		defer close(finished)
		// Preserve caller cancellation even if a host mistakenly supplies a
		// detached fence context. The host's own fence context remains in force.
		bounded, cancel := context.WithCancel(fenced)
		stop := context.AfterFunc(policyCtx, cancel)
		defer func() { stop(); cancel() }()
		if policyCtx.Err() != nil {
			return ErrUnavailable
		}
		if deadline, ok := ctx.Deadline(); ok {
			var deadlineCancel context.CancelFunc
			bounded, deadlineCancel = context.WithDeadline(bounded, deadline)
			defer deadlineCancel()
		}
		result, callbackErr = s.admit(bounded, req, key, rec, d)
		return callbackErr
	})
	visitState.Store(2)
	select {
	case <-finished:
	default:
		return unavailable(id), ErrUnavailable
	}
	if policyErr != nil || callbackErr != nil {
		return unavailable(id), ErrUnavailable
	}
	return result, nil
}

func (s *Service) saveNonexecution(ctx context.Context, key Key, r Record, id string, o genui.Outcome, challenge json.RawMessage) (genui.Response, error) {
	o, err := validOutcome(o)
	if err != nil {
		return unavailable(id), ErrUnavailable
	}
	r.Outcome = &o
	r.Challenge = append(json.RawMessage(nil), challenge...)
	r.Revision++
	if o.Status.Terminal() {
		r.Phase = PhaseTerminal
	} else {
		r.Phase = PhaseWaiting
	}
	current, ok, err := s.ports.Journal.CompareAndSwap(ctx, key, r.Revision-1, r)
	if err != nil {
		return unavailable(id), ErrUnavailable
	}
	if !ok {
		return conflict(id, current), nil
	}
	s.publish(ctx, key, r)
	return response(id, o), nil
}

func (s *Service) admit(ctx context.Context, req Request, key Key, rec Record, d Decision) (genui.Response, error) {
	id := req.Call.CallAgentFunction.FunctionCallID
	if (d.Authority == nil) == (d.Outcome == nil) {
		return unavailable(id), ErrUnavailable
	}
	if d.Outcome != nil {
		switch d.Outcome.Status {
		case genui.StatusNeedsConfirmation:
			if len(d.Challenge) == 0 || len(d.Challenge) > genui.MaxActionBytes {
				return unavailable(id), ErrUnavailable
			}
			canonical, err := genui.CanonicalJSON(d.Challenge)
			if err != nil || len(canonical) == 0 || canonical[0] != '{' {
				return unavailable(id), ErrUnavailable
			}
			d.Challenge = canonical
		case genui.StatusRefused, genui.StatusStale, genui.StatusExpired, genui.StatusSuperseded:
			if len(d.Challenge) != 0 {
				return unavailable(id), ErrUnavailable
			}
		default:
			return unavailable(id), ErrUnavailable
		}
		return s.saveNonexecution(ctx, key, rec, id, *d.Outcome, d.Challenge)
	}
	a := *d.Authority
	if a.Generation != req.Surface.Generation || a.Deadline.IsZero() || (a.Risk != genui.RiskLow && a.Risk != genui.RiskMedium && a.Risk != genui.RiskHigh) {
		return unavailable(id), ErrUnavailable
	}
	deadline := a.Deadline
	if req.Surface.ExpiresAt.Before(deadline) {
		deadline = req.Surface.ExpiresAt
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return unavailable(id), ErrUnavailable
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	claimed := rec
	claimed.Revision++
	claimed.Claimed = true
	claimed.Phase = PhaseClaimed
	claimed.Challenge = nil
	claimed.Outcome = nil
	current, ok, err := s.ports.Journal.CompareAndSwap(bounded, key, rec.Revision, claimed)
	if err != nil {
		return unavailable(id), ErrUnavailable
	}
	if !ok {
		return conflict(id, current), nil
	}
	// An expired or uncertain claim remains consumed, even if no effect started.
	if bounded.Err() != nil || !time.Now().Before(deadline) {
		return unavailable(id), ErrUnavailable
	}
	out, execErr := s.ports.Executor.Execute(bounded, Execution{Request: req, Authority: a})
	if execErr == nil {
		out, execErr = validOutcome(out)
		if execErr == nil && out.Status != genui.StatusAccepted && !out.Status.Terminal() {
			execErr = ErrUnavailable
		}
	}
	final := claimed
	final.Revision++
	if execErr != nil {
		final.Phase = PhaseUnknown
		final.Outcome = nil
	} else {
		final.Outcome = &out
		if out.Status.Terminal() {
			final.Phase = PhaseTerminal
		} else {
			final.Phase = PhasePending
		}
	}
	// Recording uncertainty/result must survive caller disconnect. This bounded
	// bookkeeping context is never used for authority or effects.
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	_, ok, err = s.ports.Journal.CompareAndSwap(recordCtx, key, claimed.Revision, final)
	if err != nil || !ok || execErr != nil {
		return unavailable(id), ErrUnavailable
	}
	s.publish(recordCtx, key, final)
	return response(id, out), nil
}

func (s *Service) publish(ctx context.Context, key Key, r Record) {
	if s.ports.Publisher != nil && r.Outcome != nil {
		_ = s.ports.Publisher.Publish(ctx, Event{Scope: key.Scope, ActionID: key.ActionID, SurfaceID: r.Request.CallAgentFunction.SurfaceID, Outcome: *r.Outcome})
	}
}

// Recover reads recorded authority; it never resubmits or infers a choice.
func (s *Service) Recover(ctx context.Context, scope Scope, actionID, invocation string) (Recovery, error) {
	if !validScope(scope) {
		return Recovery{}, ErrForbidden
	}
	if !validWireID(actionID) || !validWireID(invocation) {
		return Recovery{}, ErrInvalid
	}
	r, err := s.ports.Journal.Load(ctx, Key{scope, actionID})
	if err != nil {
		return Recovery{}, err
	}
	if !validRecord(Key{scope, actionID}, r) {
		return Recovery{}, ErrUnavailable
	}
	call := r.Request
	call.CallAgentFunction.FunctionCallID = invocation
	call.CallAgentFunction.CallFunction.Args.Action.Confirmation = ""
	b, err := json.Marshal(call)
	if err != nil {
		return Recovery{}, ErrUnavailable
	}
	call, err = genui.DecodeCall(b)
	if err != nil {
		return Recovery{}, ErrInvalid
	}
	out := Recovery{Request: &call, Response: unavailable(invocation)}
	if r.Outcome != nil {
		o, e := validOutcome(*r.Outcome)
		if e != nil {
			return Recovery{}, ErrUnavailable
		}
		out.Response = response(invocation, o)
	}
	return out, nil
}

// Contract IDs are bounded by Unicode code points, not their UTF-8 byte count.
// Validate original bytes before an ID reaches JSON encoding or a store lookup.
func validWireID(id string) bool {
	return id != "" && utf8.ValidString(id) && utf8.RuneCountInString(id) <= 256
}
