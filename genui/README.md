# GenUI contract

GenUI is a reviewed-pattern profile of A2UI 1.0 carried by the existing envelope
protocol. It contains no identity issuer, approval authority, listener, effect
executor, or durable action gateway. Those are host responsibilities.

The initial catalog contains `GenuiApproval`, `GenuiForm`, and `GenuiListDetail`.
An agent selects one of these complete patterns; it cannot supply code, raw HTML,
CSS, arbitrary components, endpoint URLs, or dynamically fetched catalogs.

## Wire and schemas

`schema/contract.schema.json` is the canonical JSON Schema 2020-12 definition
bundle. Select `$defs/Envelope`, `Call`, `Outcome`, `Response`, or
`ReceiverResponse`. `Schema(name)` returns a self-contained selected schema.
`schema/catalog.json` is the corresponding native A2UI catalog. The TypeScript
companion consumes a mechanical snapshot of this bundle and the same fixtures.
Never author a second competing contract.

The outer envelope has native fields `v`, `id`, `type`, `typeVersion`, optional
`title`, `context`, `presentation`, and `data`. Its kind is
`genui.declarative-surface`, type version `1.0.0`. `data` is one complete A2UI
`version: "1.0"` `createSurface` message. Every surface includes its initial data
model, exactly one `root` reviewed component, and matching `genui_surface` and
`genui_pattern` extension metadata. No progressive or arbitrary component tree is
accepted. The outer ID and surface ID must match.

The fixed catalog ID is `urn:hollis-labs:genui:catalog:1`. Function calls use
`callFunction: {"@call":"genui_action","args":{"genui_action":...,"input":...}}`;
`catalogId` is optional on the call and, if present, must match the fixed catalog.
RPC metadata and flat arguments are rejected. The logical action ID is separate
from the transport `functionCallId`, which changes for a new invocation.

`upstream.lock.json` pins the exact stable A2UI specification revision and hashes.
Unmodified schemas and their applicable Apache-2.0 license are under `upstream/`.
The native upstream validator accepts the custom catalog and the complete
approval, form and list-detail messages. GenUI adds its own stricter atomic-root,
numeric, resource and semantic restrictions.

## Data limits

The initial profile accepts only integer JSON numeric lexemes in the exact range
`[-9007199254740991, 9007199254740991]`. Fractional, exponential, negative-zero and
unsafe integer spellings are rejected **before** any float conversion. Decimal
amounts and larger identifiers are strings validated by the operation schema.
This is a GenUI profile restriction, not an upstream A2UI limitation.

Limits are 256 KiB per surface, 64 KiB per action/response, nesting depth 32,
16 KiB per decoded string, 256 UTF-8 bytes per object key, and 256 members per
object or items per array. Schema fields impose tighter limits: 256-character
IDs, 32 form fields, 128 list items, four approval choices, and 64 displayed
changes. `maxLength` uses Unicode code points, while the global decoded string
limit uses UTF-8 bytes. Duplicate keys, invalid UTF-8, unpaired surrogates and
trailing input refuse. No parser may silently round before validation.
Typed materializers validate original strings and map keys before JSON encoding;
malformed UTF-8 is never replaced silently. Literal U+FFFD remains valid. Typed
inputs use ordinary contract structs and JSON values, not custom JSON/text
serializers; use strict raw decoders for externally serialized data.

`CanonicalJSON` uses RFC 8785 after validating this numeric profile. It does not
choose a semantic digest projection: the host must deliberately bind the
registered operation, authenticated namespace/actor, surface, subject, basis and
input, excluding only the separately validated proof and transport invocation ID.

## Outcomes and authority

Producer statuses are `ok`, `accepted`, `needs_confirmation`, `refused`,
`conflict`, `stale`, `canceled`, `expired`, `superseded`, and `failed`.
`accepted`, `needs_confirmation`, and `stale` are nonterminal. `partial` is
unsupported. Complete submissions can produce a nonterminal result; draft edits
are not partial action submissions.

`ReadResponse` returns unknown statuses as bounded raw observations with
`Known=false`, `Terminal=false`, and no usable `Value`. A malformed known outcome
is an error. Protocol errors are not proof of terminal action failure. Unknown
or lost replies must not release a host reservation or cause automatic effect
retry. A conflict embeds the stored outcome; nested conflicts are bounded to two.

The browser cannot authorize an effect. Risk is advisory. Host identity, current
policy, target generation and lease never come from content. Confirmation is an
opaque host-issued proof bound to the same logical action and operation; it is
forbidden in published choice templates. Host policy and durable idempotency are
required before invoking effects. This package makes no exactly-once external
transaction or physical rollback claim.

## Pattern semantics

Published decision snapshots are immutable. Changed operations or review content
require a new surface and fresh review. Hiding a surface does not cancel an
operation. A toast is never the only record of a decision.

Form fields have unique keys and types `text`, `textarea`, `checkbox`, or
`integer`. Initial `dataModel.draft` may omit required fields; complete submission
may not. Present values must match declared types and constraints. Numeric bounds
apply only to integer fields, and `maxLength` only to text fields. The submit
choice's base input must not contain `draft`; the frontend adds the complete
validated local draft at `input.draft`. The host validates it and materializes a
new review snapshot. Non-form patterns may not carry a draft.

List item IDs are unique, `selectedId` must name an item, and detail requires a
selection. Pattern metadata and root component must agree. Choices are detached
copies, not registered host authority. Mutually exclusive choices may share an
action ID; the host compares the full operation digest and does not choose a
request merely by ID.

Presentation and focus behavior belong to the design kit host: modal, drawer with
side, popover and toast; overlay depth two with only confirmation on top; docked
regions separate. Unsupported envelope presentation hints must degrade rather
than give content control of arbitrary regions. This profile accepts the native
inline/modal/drawer/fullscreen hints; it introduces no legacy aliases.

## Verification

Run `go test -race -count=1 ./genui` and `go vet ./genui`.
`testdata/fixtures/manifest.json` is the shared cross-language conformance manifest.
Fixtures cover producer/receiver distinctions, malformed shapes, safe numbers,
semantic pattern checks and complete reviewed surfaces. Runtime admission,
concurrent execution, journal recovery and live integration require separate
host tests; fixture conformance is not evidence that those capabilities exist.
