package kinds

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContentIdentity is shared by authored definitions. IDs are opaque native
// identifiers: canonicalization prefixes the kind and never cleans paths,
// folds case, or turns a reference into a local identity.
type ContentIdentity struct {
	Schema   string         `json:"schema"`
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Summary  string         `json:"summary,omitempty"`
	Owner    string         `json:"owner,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type VersionedReference struct {
	Reference Reference `json:"reference"`
	Revision  string    `json:"revision"`
}

// AgentDefinition describes authored content, never launch authority. Profile
// resolution and permission semantics remain with the referenced owner.
type AgentDefinition struct {
	Charter       string              `json:"charter"`
	Roles         []string            `json:"roles"`
	Skills        []Reference         `json:"skills,omitempty"`
	Tools         []Reference         `json:"tools,omitempty"`
	References    []Reference         `json:"references,omitempty"`
	LaunchProfile *VersionedReference `json:"launch_profile,omitempty"`
}
type Agent struct {
	ContentIdentity
	AgentDefinition
}
type Cell struct {
	Name       string           `json:"name"`
	Definition *AgentDefinition `json:"definition,omitempty"`
	Reference  *Reference       `json:"reference,omitempty"`
}
type Agency struct {
	ContentIdentity
	Framework  string      `json:"framework"`
	Charter    string      `json:"charter"`
	Cells      []Cell      `json:"cells"`
	References []Reference `json:"references,omitempty"`
}
type Skill struct {
	ContentIdentity
	Instructions string              `json:"instructions"`
	InputSchema  json.RawMessage     `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage     `json:"output_schema,omitempty"`
	Tools        []Reference         `json:"tools,omitempty"`
	Documents    []Reference         `json:"documents,omitempty"`
	Source       *VersionedReference `json:"source,omitempty"`
}
type Document struct {
	ContentIdentity
	Subtype       string      `json:"subtype"`
	Format        string      `json:"format"`
	Body          string      `json:"body"`
	Purpose       string      `json:"purpose,omitempty"`
	Applicability string      `json:"applicability,omitempty"`
	References    []Reference `json:"references,omitempty"`
}
type Workstream struct {
	ContentIdentity
	Status          string      `json:"status"`
	WorkflowID      string      `json:"workflow_id,omitempty"`
	ProjectURNs     []string    `json:"project_urns,omitempty"`
	SourceCreatedAt string      `json:"source_created_at,omitempty"`
	SourceUpdatedAt string      `json:"source_updated_at,omitempty"`
	References      []Reference `json:"references,omitempty"`
}
type Correlation struct {
	ContentIdentity
	Links     map[string]Reference  `json:"links"`
	SourceIDs *CorrelationSourceIDs `json:"source_ids,omitempty"`
	Trace     *TraceReference       `json:"trace,omitempty"`
}
type CorrelationSourceIDs struct {
	CorrelationID string `json:"correlation_id,omitempty"`
	InReplyTo     string `json:"in_reply_to,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}
type TraceReference struct {
	TraceID        string `json:"trace_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
	Traceparent    string `json:"traceparent,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Depth          *int64 `json:"depth,omitempty"`
}

// CanonicalContentID applies only to the six content schemas; project and tool
// have their own approved identity rules. Native IDs remain exact after ':';
// transport adapters must encode the whole value rather than path-clean it.
func CanonicalContentID(schema, id string) (string, error) {
	switch schema {
	case AgentSchema, AgencySchema, SkillSchema, DocumentSchema, WorkstreamSchema, CorrelationSchema:
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownSchema, schema)
	}
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "\x00\r\n") {
		return "", fmt.Errorf("id: nonempty opaque identifier without NUL/newline required")
	}
	return strings.TrimSuffix(schema, "/1") + ":" + id, nil
}

// ParseContentJSON returns one of *Agent, *Agency, *Skill, *Document,
// *Workstream or *Correlation after strict structural validation. References
// may be unavailable; their existence or authority is not certified here.
func ParseContentJSON(schema string, data []byte) (any, error) {
	var out any
	switch schema {
	case AgentSchema:
		out = &Agent{}
	case AgencySchema:
		out = &Agency{}
	case SkillSchema:
		out = &Skill{}
	case DocumentSchema:
		out = &Document{}
	case WorkstreamSchema:
		out = &Workstream{}
	case CorrelationSchema:
		out = &Correlation{}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownSchema, schema)
	}
	if err := decode(schema, data, out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateContent(data []byte, schema string) error {
	var identity ContentIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return err
	}
	if _, err := CanonicalContentID(schema, identity.ID); err != nil {
		return err
	}
	if schema == AgencySchema {
		var a Agency
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, cell := range a.Cells {
			if seen[cell.Name] {
				return fmt.Errorf("cells: duplicate name %q", cell.Name)
			}
			seen[cell.Name] = true
		}
	}
	return nil
}
