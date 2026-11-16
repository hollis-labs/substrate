# Host-owned GenUI gateway

The gateway connects the reviewed GenUI contract to an application's own
identity, persistence, policy and effects. It is a library and an optional
Substrate module; it does not start a listener or create a decision service.

Supply `PatternCatalog`, `SurfaceStore`, `ActionJournal`, `Policy` and `Executor`
to `gateway.New`. `Publisher` is optional advisory delivery. There is no memory
journal, permissive policy, confirmation issuer or production database adapter.
The SQLite adapters in tests are isolated fixtures, not production adapters.

The catalog materializes only reviewed `approval`, `form` and `list_detail`
parameters. It returns a complete validated envelope plus host-only operation
bindings, generation and expiry. `ChoiceDigest` binds each offered choice to a
registered operation. Form submissions add only the complete validated `draft`
to the published base input. A host form operation prepares a new review surface
for consequential changes; it must not silently treat preparation as approval.
Agent and plugin entry points call this same service using an authenticated
host-resolved scope. They cannot supply URLs, arbitrary components, tool names
or trusted runtime facts through the action wire.

## Admission and durability

The journal key is canonical actor + namespace + logical action ID. Credential
aliases must resolve to the same actor. The semantic digest binds the surface,
catalog, function, subject, basis, complete input and host operation/generation.
Transport invocation ID, advisory client risk and confirmation proof are not
semantic identity. Same-ID changed submissions conflict; terminal command
repeats return `conflict` with the recorded outcome. They never execute again.

`Policy.WithFence` rechecks current actor enrollment, actual risk, grants, basis,
generation/lease, expiry and any confirmation proof. It calls the provided
closure synchronously exactly once, retaining its revocation/admission fence
through the actual `Executor.Execute` operation. The executor must honor the
bounded context through its physical effect. A browser click or client risk
hint cannot establish stronger presence. Missing host adapters refuse.

The gateway persists a claim before entering the executor. Errors, cancellation,
lost commit acknowledgements, unknown effects and restart never release it.
Journal adapters must atomically enforce immutable request/digest, monotonically
increasing revisions and nondecreasing claim state. A nil storage return means
durable acknowledgement; an error must not imply rollback or authorize an effect
retry. This is not an exactly-once transaction across an external system.

A confirmation challenge consumes no execution claim. Its host-private binding
is retained in the journal; proof verification remains with the real host
adapter. Proof is excluded from the persisted replayable request and recovery.
Accepted asynchronous work retains its claim and opaque handle. The host may
complete that record by exact journal CAS after its own domain reconciliation;
recovery and duplicate submissions never start that work again. Unknown claims
require host reconciliation and cannot be automatically retried.

Outcome/uncertainty bookkeeping has a bounded cancellation-independent context
after execution, solely to survive client disconnect. It cannot grant authority
or execute effects. Publication happens after durable result acknowledgement;
a publisher failure does not erase or rerun a known result.

## HTTP and recovery

`gateway.NewHTTP` mounts handlers through the application's existing host:

| Method and path | Explicit scope |
| --- | --- |
| `POST /api/genui/surfaces` | `{app}.genui.show` |
| `POST /api/genui/actions` | `{app}.genui.act` |
| `GET /api/genui/actions/{actionID}?functionCallId=...` | `{app}.genui.read` |

POST bodies are bounded JSON. Show accepts only `{pattern,params}` and passes
parameters to the host's reviewed catalog. Action accepts the shared native
`genui.Call`. Recovery returns `{request?,response}`: the canonical original
choice/input/form draft with proof removed and the stored native response. Both
messages use the fresh requested transport invocation ID. A UI may display a
response without the request, but cannot infer a choice or confirm/resubmit.
An unavailable/unknown response authorizes only another read, not execution.

Handlers require a verified principal, explicit scope and current canonical
host mapping even if an outer guard is permissive/off or a module route-scope
override is broader. Principal expiry bounds the action context. Request JSON
never chooses actor or namespace. Responses disable HTTP caching.

## Optional module

Select `genui` explicitly and register `modules/genui.UseHost` before boot.
The module requires the existing `auth` and `httphost` capabilities and starts
no workers or listeners. Removing it removes its routes and UI contribution.

The UI contribution declares `@hollis-labs/genui` and
`genui.declarative-surface@1.0.0`. No unpublished version is invented in the BOM.
The Go capability can be linked and tested independently; frontend generation
fails explicitly until a real owner-approved package release is pinned.
Existing approval cards, standing grants and other application workflows are
not implicitly connected. A real host adapter is a separate integration step.

`RegisterEnvelope` registers the canonical surface data schema through the
existing namespaced `envelopes.Registry` extension API. It does not change the
core envelope catalog. Continue validating raw input with `genui.DecodeEnvelope`
for resource bounds and cross-field semantics before registry use.

`ShowPatternTool` is an agent-facing raw-JSON adapter named `genui_show_pattern`.
The host mounts `InputSchema` and `Handle` with the explicit show scope and a
write annotation because showing persists a surface. It must preserve original
argument bytes. The current generic go-mcp map handler decodes through float64
and cannot prove numeric lexemes or duplicate keys; it is not silently used as
this adapter. No MCP transport or server is automatically activated.
