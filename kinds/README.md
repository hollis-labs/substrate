# Shared operational kinds

`kinds` is a pure contract package for FC-8 / TR-1
(CW-20261115-0067, CW-20261114-0170). It supplies project/1 RC1 and tool/1
authored payloads, classification values, directional graph relations, JSON
Schemas, strict parsers and deterministic definition hashes. Registry owns
records, revisions, storage, publication authority and review decisions.

No boot registration or running Substrate host is needed. Import
`github.com/hollis-labs/substrate/kinds` from generators, CI or services.
It does not change Folio descriptors, register tools or migrate consumers.

## Entry points

- `Schema(version)` and `Schemas()`: embedded draft 2020-12 JSON Schemas.
- `ParseProjectJSON` / `ParseProjectYAML`: the same project contract;
  `Project.Validate` for Go values.
- `ParseToolJSON` / `Tool.Validate`: authored definitions and canonical identity.
- `ParseRelationJSON` / `Relation.Validate`: typed reference/edge shape.
- `Classification.Validate`: classification values, without authorization.
- `ValidateJSON`: schema validation, plus tool identity consistency.
- `ToolID`, `Project.CanonicalID`: globally scoped canonical identities.
- `DefinitionProjection`, `DefinitionHash`: TR-1 hash bytes and lowercase
  SHA-256 hex. `HashAlgorithm` is `tr1-jcs-sha256/1`.
- `EdgeSpecs`, `Inverse`: stored edge directions and their defined inverses.

Schema/parse errors retain property context; unknown schema versions wrap
`ErrUnknownSchema`. Consumers must bound incoming request sizes before parsing.
Validation rejects unknown properties, duplicate JSON/YAML keys, wrong enum
values and wrong field types. Arbitrary authored data is confined to
`settings` / `metadata`, tool `meta`, and actual JSON Schema bodies.
It does not resolve remote references or interpret embedded tool schemas.

Project parsing, `Validate` and `CanonicalID` check descriptor structure,
**not class-bar certification**. Registry can describe a class 1 project
without an operator/enforced authentication or a class 2 CLI with auth `none`.
The class 1 operator/auth bar and class 2 development-only loopback rules belong
to conformance, promotion and runtime checks. Adopting this parser in Folio must
preserve its existing static/runtime checks; replacing those with shape
validation alone would weaken them. No policy engine is supplied here.

YAML support is parse-only. `yaml.Marshal(Project)` is not a supported
serializer; use `json.Marshal` for typed roundtrips or retain authored YAML.
Numeric scalar lexemes are checked before YAML float conversion, including
nested/anchored values, so nonzero underflow cannot silently become zero.
Quoted and explicitly tagged strings retain their string meaning.

Project class is an integer 1/2/3 or the string `lib`, not a numeric string.
Library interfaces have type `lib`. Extension settings remain intact; the
package neither defaults runtime paths nor applies environment-specific
authentication policy. `last_verified` and other service stamps are not
project.yaml fields. Breaking schema versions need a pure migration when a real
predecessor exists; none is invented for this first version. Project/1 remains
RC1 and may change before stabilization.

## Identity and references

A project slug stays in the payload: `sample` becomes canonical ID
`project:sample`. Tool IDs preserve TR-1:
`tool:mcp/example/lookup`, `tool:builtin/claude/Bash`,
`tool:cli/git/push`. Non-CLI identities use source/origin/name; CLI identities
use the program/subcommand signature with no origin, executable path or flags.
No case folding, path cleaning or alias substitution is performed.
MCP names follow the SDK grammar: 1–128 ASCII letters, digits, underscores,
hyphens or periods in any position, including punctuation prefixes. They are
protocol names, not CLI path segments; consumers must preserve them exactly.

These IDs have **one global uniqueness domain**. A service must reject a
second authority claiming the same ID; authority does not silently qualify it.
It must also enforce alias uniqueness across records for `(system, alias)`.
This package can reject duplicates within a payload, not query other records.

A `Reference` carries authority, kind and opaque native ID, optionally a URI.
Native IDs are preserved and are not automatically Registry IDs. Reference
existence and explicit unresolved-reference admission belong to the service.
The package never invents target records.

FC-8 defines owned_by, operated_by, responsible_for, answers_to, consumes,
part_of and supersedes, with their published inverse names. Only source
directions are stored. Their endpoint-kind sets remain open because RC1 has
not narrowed them. TR-1 selection edges originate at tools; related_skill and
related_doc target the respective content kinds, requires can point to any
kind, and the other selection edges point to tools. None has an inferred
symmetric or transitive closure. Mined relations start proposed. This is
validation of a value, not evidence that a caller can publish it as reviewed.

## Definition hash contract

For non-CLI tools, hash exactly the present fields
`{name, description, input_schema, output_schema, annotations}`.
Title, icons, meta, aliases and provenance are excluded, but should still
produce immutable service revisions when changed.
Here title means the top-level definition title. The complete annotations
object, including MCP `annotations.title`, participates in the hash.

For CLI tools, hash the grammar
`{signature, help_text_digest, known_flags}`. The help digest is lowercase
SHA-256 hex; flags are an ordered unique array. Runtime argument values,
binary paths and observations are not grammar.

Optional definition fields use `json.RawMessage`: nil means absent,
`json.RawMessage("null")` means explicit null. Neither becomes the other.
The MCP input schema must be an object with `type: object`. Declared hint
absence, null, false and true remain distinct, without effective defaults.
Arrays retain order; object ordering and equivalent numeric notation do not
change a hash.

Canonical bytes use [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785.html)
through the BOM-pinned
[Cyberphone Go implementation](https://github.com/cyberphone/json-canonicalization/tree/master/go).
This is not a replacement encoder. The accepted numeric domain is finite
binary64 with an additional admission rule: integral values outside
[-9007199254740991, 9007199254740991], overflow, and nonzero underflow to zero
are rejected. Fractional values use JCS binary64 rounding; exact decimal,
larger integer or smaller nonzero data must be strings. Invalid Unicode and
duplicate keys are rejected before a hash can be issued.

Golden vectors cover absent/null, schema numbers and escaping, and CLI grammar.
Other behavioral tests exercise reordered object keys, included/excluded fields,
hint states and invalid number/Unicode inputs. A changed definition hash must
invalidate effective review **in the service transaction**. Generic authored
tool payloads reject effective/reviewer fields, but this package does not
authenticate an approver or implement the review queue.

## Validation

```sh
go test -race ./kinds/...
go vet ./...
go test ./...
go run ./cmd/substrate pins check
```

### Authored content contracts

`agent/1`, `agency/1`, `skill/1`, `document/1`, `workstream/1` and
`correlation/1` add retrievable authored definitions. `ParseContentJSON` and
`ValidateJSON` enforce the same strict shape. Their canonical IDs prefix
`<kind>:` to the exact native payload ID; punctuation, slashes and case are
preserved. They are independent of the owning authority. Existence, uniqueness,
lifecycle and review authorization remain service responsibilities.

Agent bodies and roles are required; launch profiles are versioned references to
an external authority, with no copied permission schema. Citadel agencies carry
bounded named cells containing either an inline agent definition or a typed
reference. These are minimal authored contracts, not an execution engine or a
claim of a pre-existing normative Citadel schema. Skills retain instructions;
documents retain Markdown/plain-text bodies and explicit supported subtypes.
Workstreams retain source identifiers/times independently of storage timestamps.
Correlation links qualify native IDs by authority and kind; correlation, reply,
causation and trace identifiers remain distinct. Runtime leases, credentials and
session execution state are not accepted definition fields.

Specialized authored-definition slots constrain their kind (tool, skill,
document, agent, launch_profile). Correlation link names describe roles, not an
authoritative target-kind vocabulary: every correlation slot retains a generic
typed external reference. Native authority, kind and ID are preserved without
inventing adapter mappings. Integer-valued JSON depth
spellings (`1`, `1.0`, `1e0`) have equal semantics and decode without truncation;
fractional, unsafe and underflowed numbers fail admission.

Workstream source names may be absent or empty, as permitted by the existing
source. Typed decoding represents both as an empty name; callers may display an
ID fallback but must not persist an invented source name. Replacement APIs need
not adopt the source's PATCH omission-versus-clear command semantics. Project
URN omission and an empty array both describe no project memberships here.

### Registry service, config and knowledge contracts

`service/1`, `config/1`, `knowledge/1`, `mcp_server/1` and
`launch_profile/1` use the same `ParseContentJSON`, `CanonicalContentID`,
`ValidateJSON` and schema discovery API. Typed values also provide `Validate`.
These contracts describe authored intent; they confer no publication, launch,
deployment or contact authority. Registry owns revisions, approval and target
existence. Final schema acceptance and consumer activation are separate gates.

- Services identify their project, host, Cerberus resource, owner/operator,
  interfaces and deployment method (`fleet-install` or `cerberus`). Port and
  health URL are optional: non-listening processes do not get an invented port
  or probe. An empty interface list is explicit. The contract does not attest
  that a described service is running or healthy.
- Config targets one project, service, agent or agency, with a matching `scope`.
  `settings_schema` is an embedded JSON Schema object with `type: object`;
  `settings` must validate against it. Local `$defs`/references work; external
  schema loads are refused and no defaults are applied. Validation uses the
  existing JSON Schema library and shared numeric domain. Application-owned
  live config remains in the application's database. `secret_refs` maps field
  names to typed `{authority, kind: "secret", id}` locators.
- Knowledge is standalone authored content with a Markdown or plain-text body,
  nonempty provenance references and an optional versioned `upstream` reference
  to Tesseract. Temporary sync must preserve upstream authority, revisions and
  drift rather than push local edits back. Import checkpoints and body digests
  belong to the consuming service, not mutable fields invented by this package.
- MCP servers retain transport plus an exact versioned Tether-owned source
  reference; optional tool/config references describe relationships. Launch
  profiles retain an exact versioned Tether-owned launch-profile source.
  Neither copies Tether's native execution/permission schema. Reference shape
  validation does not prove the source revision exists or is executable.

`deployed_as` stores project → service; `configures` stores config →
project/service/agent/agency. Both constrain endpoint kinds and have no inferred
inverse. Service → project uses the existing `part_of` relation. Existing FC-8
relations remain available to all new kinds. No relation implies transitive
closure, grants or an operational binding. Cross-record consistency (including
agreement between embedded references and authored edges) is a Registry check.

All five new kinds call `ValidateNoSecretsJSON` before structural validation.
The helper is also available for service envelopes and earlier kinds. It uses
`libs/security/redact` credential detection over decoded strings (including
free text, URLs and nested metadata); sensitive property names require a typed
secret reference, never a literal or placeholder. This path refuses input,
never silently scrubs it. Errors contain no rejected credential values.
Secret references are never resolved. Detection is deliberately conservative:
it cannot establish that every opaque string is safe. Services must bound
request sizes, screen source projections, and avoid logging rejected inputs.

`EdgeSpec.AllowsCycle` and `AllowsCycle(type)` provide the explicit graph cycle
policy. Only `consumes`, `related_doc`, `related_skill` and `conflicts_with`
permit directed cycles. Every other declared relation forbids cycles; unknown
relation types return an error and false. A mixed-relation cycle is admissible
only if **every** participating edge permits it. Self-edges are cycles too.
Endpoint-kind constraints still apply. The service detects paths and enforces
this against its complete transaction-local graph, including same-batch edges;
validating an individual relation alone does not certify graph acyclicity.
This policy does not imply transitive closure or operational permission.
