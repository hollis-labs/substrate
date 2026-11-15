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
