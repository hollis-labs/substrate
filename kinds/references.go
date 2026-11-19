package kinds

import (
	"fmt"
	"sort"
)

// SemanticReference is an authored outgoing dependency, before service-owned
// resolution to a canonical ID and immutable target revision. Native reference
// IDs remain opaque. This value supplies neither existence nor authorization.
type SemanticReference struct {
	Relation  EdgeType  `json:"relation"`
	Reference Reference `json:"reference"`
}

// SemanticReferences validates and extracts the five Registry content kinds'
// semantic dependencies. Descriptive references, ownership text, secret
// locators, source/upstream and provenance are deliberately excluded. Returned
// dependencies are deduplicated and sorted without modifying payload order.
// Services must replace stale generated edges and resolve/pin targets within
// their transaction; this function does not create stored graph relations.
func SemanticReferences(schema string, data []byte) ([]SemanticReference, error) {
	if !isRegistryKind(schema) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSchema, schema)
	}
	content, err := ParseContentJSON(schema, data)
	if err != nil {
		return nil, err
	}
	refs := []SemanticReference{}
	add := func(relation EdgeType, ref Reference) { refs = append(refs, SemanticReference{relation, ref}) }
	switch v := content.(type) {
	case *Service:
		add(PartOf, v.Project)
		for _, ref := range v.Config {
			add(Consumes, ref)
		}
	case *Config:
		add(Configures, v.Target)
	case *MCPServer:
		for _, ref := range v.Tools {
			add(Consumes, ref)
		}
		for _, ref := range v.Config {
			add(Consumes, ref)
		}
	}
	less := func(a, b SemanticReference) bool {
		x := [...]string{string(a.Relation), a.Reference.Authority, a.Reference.Kind, a.Reference.ID, a.Reference.URI}
		y := [...]string{string(b.Relation), b.Reference.Authority, b.Reference.Kind, b.Reference.ID, b.Reference.URI}
		for i := range x {
			if x[i] != y[i] {
				return x[i] < y[i]
			}
		}
		return false
	}
	sort.Slice(refs, func(i, j int) bool { return less(refs[i], refs[j]) })
	unique := refs[:0]
	for _, ref := range refs {
		if len(unique) == 0 || unique[len(unique)-1] != ref {
			unique = append(unique, ref)
		}
	}
	return unique, nil
}
