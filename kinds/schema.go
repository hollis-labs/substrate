package kinds

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	ProjectSchema        = "project/1"
	ToolSchema           = "tool/1"
	RelationSchema       = "relation/1"
	ClassificationSchema = "classification/1"
	AgentSchema          = "agent/1"
	AgencySchema         = "agency/1"
	SkillSchema          = "skill/1"
	DocumentSchema       = "document/1"
	WorkstreamSchema     = "workstream/1"
	CorrelationSchema    = "correlation/1"
)

var ErrUnknownSchema = errors.New("unknown kind schema")

// ValidateJSONValue checks JSON syntax, duplicate keys, Unicode and the same
// finite binary64/safe-integer numeric domain used by kind payloads and hashes.
// Services use it for envelopes and extensible metadata before native decoding
// can erase unsafe numeric lexemes. It imposes no object shape or kind schema.
func ValidateJSONValue(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	// The canonicalizer accepts arrays/objects at the root; a one-element
	// array admits scalar values using the identical nested-value policy.
	wrapped := append([]byte{'['}, data...)
	wrapped = append(wrapped, ']')
	_, err := canonicalJSON(wrapped)
	return err
}

//go:embed schemas/*.json
var schemas embed.FS

var schemaFiles = map[string]string{
	AgentSchema:       "schemas/agent-1.json",
	AgencySchema:      "schemas/agency-1.json",
	SkillSchema:       "schemas/skill-1.json",
	DocumentSchema:    "schemas/document-1.json",
	WorkstreamSchema:  "schemas/workstream-1.json",
	CorrelationSchema: "schemas/correlation-1.json",

	ProjectSchema:        "schemas/project-1.json",
	ToolSchema:           "schemas/tool-1.json",
	RelationSchema:       "schemas/relation-1.json",
	ClassificationSchema: "schemas/classification-1.json",
}

// Schemas returns the supported versions in deterministic order.
func Schemas() []string {
	return []string{ProjectSchema, ToolSchema, RelationSchema, ClassificationSchema, AgentSchema, AgencySchema, SkillSchema, DocumentSchema, WorkstreamSchema, CorrelationSchema}
}

// Schema returns an independent copy of the JSON Schema document.
func Schema(version string) ([]byte, error) {
	path, ok := schemaFiles[version]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSchema, version)
	}
	return schemas.ReadFile(path)
}

type compiledSchema struct {
	once     sync.Once
	resolved *jsonschema.Resolved
	err      error
}

var compiled = map[string]*compiledSchema{
	ProjectSchema: {}, ToolSchema: {}, RelationSchema: {}, ClassificationSchema: {},
	AgentSchema:       {},
	AgencySchema:      {},
	SkillSchema:       {},
	DocumentSchema:    {},
	WorkstreamSchema:  {},
	CorrelationSchema: {},
}

// ValidateJSON checks strict wire shape without applying defaults or discarding
// unknown fields. Error messages include the schema property path. Existence,
// uniqueness across records, authorization and lifecycle transitions are service
// responsibilities. JSON numbers use the binary64 domain described in README.
func ValidateJSON(version string, data []byte) error {
	entry, ok := compiled[version]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSchema, version)
	}
	canonical, err := canonicalJSON(data)
	if err != nil {
		return fmt.Errorf("%s: %w", version, err)
	}
	entry.once.Do(func() {
		raw, err := Schema(version)
		if err != nil {
			entry.err = err
			return
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			entry.err = err
			return
		}
		entry.resolved, entry.err = s.Resolve(nil)
	})
	if entry.err != nil {
		return entry.err
	}
	var value any
	if err := json.Unmarshal(canonical, &value); err != nil {
		return err
	}
	if err := entry.resolved.Validate(value); err != nil {
		return fmt.Errorf("%s: %w", version, err)
	}
	if version == ToolSchema {
		var t Tool
		if err := json.Unmarshal(data, &t); err != nil {
			return err
		}
		return t.validateIdentity()
	}
	switch version {
	case AgentSchema, AgencySchema, SkillSchema, DocumentSchema, WorkstreamSchema, CorrelationSchema:
		return validateContent(data, version)
	}
	return nil
}

func decode(version string, data []byte, out any) error {
	if err := ValidateJSON(version, data); err != nil {
		return err
	}
	// Decode original bytes, retaining absent/null and raw definition fields.
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func validateValue(version string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return ValidateJSON(version, raw)
}
