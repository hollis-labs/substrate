package kinds

import "fmt"

type EdgeType string

const (
	DeployedAs     EdgeType = "deployed_as"
	Configures     EdgeType = "configures"
	OwnedBy        EdgeType = "owned_by"
	OperatedBy     EdgeType = "operated_by"
	ResponsibleFor EdgeType = "responsible_for"
	AnswersTo      EdgeType = "answers_to"
	Consumes       EdgeType = "consumes"
	PartOf         EdgeType = "part_of"
	Supersedes     EdgeType = "supersedes"
	ComposesWith   EdgeType = "composes_with"
	UseInstead     EdgeType = "use_instead"
	Requires       EdgeType = "requires"
	RelatedSkill   EdgeType = "related_skill"
	RelatedDoc     EdgeType = "related_doc"
	AlternativeFor EdgeType = "alternative_for"
	Precedes       EdgeType = "precedes"
	ConflictsWith  EdgeType = "conflicts_with"
)

// EdgeSpec declares a stored direction. Only FC-8 has agreed named inverses;
// the TR-1 selection relations are directional and have no inferred inverse.
type EdgeSpec struct {
	Type        EdgeType
	Inverse     string
	SourceKinds []string // empty = any declared kind
	TargetKinds []string // empty = any declared kind
	// AllowsCycle explicitly admits this relation in a directed cycle. Services
	// enforce graph-wide checks; false is the default for dependency relations.
	AllowsCycle bool
}

func EdgeSpecs() []EdgeSpec {
	return []EdgeSpec{
		{DeployedAs, "", []string{"project"}, []string{"service"}, false},
		{Configures, "", []string{"config"}, []string{"project", "service", "agent", "agency"}, false},
		{OwnedBy, "owns", nil, nil, false},
		{OperatedBy, "operates", nil, nil, false},
		{ResponsibleFor, "has_responsible", nil, nil, false},
		{AnswersTo, "oversees", nil, nil, false},
		{Consumes, "consumed_by", nil, nil, true},
		{PartOf, "contains", nil, nil, false},
		{Supersedes, "superseded_by", nil, nil, false},
		{ComposesWith, "", []string{"tool"}, []string{"tool"}, false},
		{UseInstead, "", []string{"tool"}, []string{"tool"}, false},
		{Requires, "", []string{"tool"}, nil, false},
		{RelatedSkill, "", []string{"tool"}, []string{"skill"}, true},
		{RelatedDoc, "", []string{"tool"}, []string{"document"}, true},
		{AlternativeFor, "", []string{"tool"}, []string{"tool"}, false},
		{Precedes, "", []string{"tool"}, []string{"tool"}, false},
		{ConflictsWith, "", []string{"tool"}, []string{"tool"}, true},
	}
}

// AllowsCycle reports the explicit relation policy; unknown types fail closed.
// A directed cycle is admitted only when every edge on that cycle allows it,
// including mixed-relation cycles. This function does not traverse a graph.
// Self-edges count as cycles and use the same policy and endpoint constraints.
func AllowsCycle(t EdgeType) (bool, error) {
	for _, s := range EdgeSpecs() {
		if s.Type == t {
			return s.AllowsCycle, nil
		}
	}
	return false, fmt.Errorf("unknown edge type %q", t)
}

// Reference retains an external authority's opaque native identifier. ID is
// not implicitly a Registry canonical ID, even if it contains a colon.
type Reference struct {
	Authority string `json:"authority"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	URI       string `json:"uri,omitempty"`
}

type SelectionContext struct {
	Harness   string `json:"harness,omitempty"`
	RoleCell  string `json:"role/cell,omitempty"`
	TaskClass string `json:"task_class,omitempty"`
	Workflow  string `json:"workflow,omitempty"`
	Domain    string `json:"domain,omitempty"`
}

type Relation struct {
	Schema   string            `json:"schema"`
	Type     EdgeType          `json:"type"`
	From     Reference         `json:"from"`
	To       Reference         `json:"to"`
	Weight   *float64          `json:"weight,omitempty"`
	Context  *SelectionContext `json:"context,omitempty"`
	Source   string            `json:"source"`
	Status   string            `json:"status"`
	Evidence []Reference       `json:"evidence,omitempty"`
}

func ParseRelationJSON(data []byte) (*Relation, error) {
	var r Relation
	if err := decode(RelationSchema, data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r Relation) Validate() error { return validateValue(RelationSchema, r) }

func Inverse(t EdgeType) (string, error) {
	for _, s := range EdgeSpecs() {
		if s.Type == t {
			return s.Inverse, nil
		}
	}
	return "", fmt.Errorf("unknown edge type %q", t)
}
