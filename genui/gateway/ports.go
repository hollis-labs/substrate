// Package gateway coordinates reviewed GenUI actions through host-owned
// persistence, identity, policy and execution. It supplies no authority or store.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/substrate/genui"
)

var (
	ErrUnavailable = errors.New("genui gateway unavailable")
	ErrNotFound    = errors.New("genui scoped record not found")
	ErrInvalid     = errors.New("genui invalid request")
	ErrForbidden   = errors.New("genui forbidden")
)

// Scope must come from an authenticated host mapping, never action JSON. Alias
// credentials for one actor must resolve to the same canonical scope.
type Scope struct {
	Namespace string
	Actor     string
}
type Key struct {
	Scope    Scope
	ActionID string
}

// ShowRequest names reviewed parameters, not an operation, URL or component
// tree. The host catalog validates its application-specific parameter schema.
type ShowRequest struct {
	Pattern genui.Pattern   `json:"pattern"`
	Params  json.RawMessage `json:"params"`
}

type OperationBinding struct {
	ChoiceDigest string
	Operation    string
}

// Surface is a host-retained immutable snapshot. Operations, generation and
// expiry are trusted host fields and never accepted from the client wire.
type Surface struct {
	Scope      Scope
	Envelope   genui.Envelope
	Operations []OperationBinding
	Generation string
	ExpiresAt  time.Time
	Superseded bool
}

type PatternCatalog interface {
	Materialize(context.Context, Scope, ShowRequest) (Surface, error)
}

// SurfaceStore must persist immutable snapshots. Put may accept an identical
// existing snapshot but must reject reuse of an ID with different content.
// Supersede is an exact-generation CAS; it never edits published content.
type SurfaceStore interface {
	Put(context.Context, Surface) error
	Get(context.Context, Scope, string) (Surface, error)
	Supersede(context.Context, Scope, string, string) error
}

type Phase string

const (
	PhaseReady    Phase = "ready"
	PhaseWaiting  Phase = "waiting"
	PhaseClaimed  Phase = "claimed"
	PhasePending  Phase = "pending"
	PhaseTerminal Phase = "terminal"
	PhaseUnknown  Phase = "unknown"
)

// Record contains a proof-free original request. Challenge is host-private and
// never returned by recovery. A claim is permanent, including after uncertainty.
type Record struct {
	Revision  uint64
	Digest    string
	Request   genui.Call
	Phase     Phase
	Claimed   bool
	Outcome   *genui.Outcome
	Challenge json.RawMessage `json:",omitempty"`
}

// ActionJournal is the durability boundary. Begin atomically inserts revision
// 1 or returns the existing record. CAS changes exactly the expected revision
// and returns the current record on contention. Implementations must never
// change Digest/Request, clear Claimed, or delete/reuse an action key. A nil
// return acknowledges durable commit; uncertain commits return an error and
// must not be retried as effects. No in-memory production default is provided.
type ActionJournal interface {
	Begin(context.Context, Key, Record) (Record, bool, error)
	Load(context.Context, Key) (Record, error)
	CompareAndSwap(context.Context, Key, uint64, Record) (Record, bool, error)
}

type Request struct {
	Scope     Scope
	Surface   Surface
	Call      genui.Call
	Operation string
	Digest    string
	Previous  Record
}

// Authority is freshly derived by the host. Client risk and browser clicks are
// not proof. The host must re-read current grants, actor, basis, generation and
// confirmation binding while holding the fence through the physical effect.
type Authority struct {
	Generation string
	Deadline   time.Time
	Risk       genui.Risk
}
type Decision struct {
	Authority *Authority
	Outcome   *genui.Outcome
	Challenge json.RawMessage
}

// Policy calls visit synchronously exactly once while holding its admission
// fence. It supplies either current authority or a nonexecuting outcome. Proof
// verification binds actor/action/input/subject/scope/basis/generation/expiry;
// the gateway neither issues proofs nor upgrades presence from a click. The
// callback context must retain the host's cancellation/deadline/revocation fence.
type Policy interface {
	WithFence(context.Context, Request, func(context.Context, Decision) error) error
}

type Execution struct {
	Request   Request
	Authority Authority
}
type Executor interface {
	Execute(context.Context, Execution) (genui.Outcome, error)
}

type Event struct {
	Scope     Scope
	ActionID  string
	SurfaceID string
	Outcome   genui.Outcome
}

// Publisher is advisory delivery after durable state, not an authority store.
type Publisher interface {
	Publish(context.Context, Event) error
}

type Ports struct {
	Catalog   PatternCatalog
	Surfaces  SurfaceStore
	Journal   ActionJournal
	Policy    Policy
	Executor  Executor
	Publisher Publisher
}

// Recovery retains the exact selected choice and full form draft. Request is
// optional for adapters that cannot safely disclose it; such a response may be
// displayed but must not be used to infer a choice, confirm or resubmit.
type Recovery struct {
	Request  *genui.Call    `json:"request,omitempty"`
	Response genui.Response `json:"response"`
}
