package kinds_test

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestEdgesAndReferences(t *testing.T) {
	t.Parallel()
	for edge, inverse := range map[kinds.EdgeType]string{
		kinds.OwnedBy: "owns", kinds.OperatedBy: "operates", kinds.ResponsibleFor: "has_responsible",
		kinds.AnswersTo: "oversees", kinds.Consumes: "consumed_by", kinds.PartOf: "contains", kinds.Supersedes: "superseded_by",
	} {
		got, err := kinds.Inverse(edge)
		if err != nil || got != inverse {
			t.Fatalf("%s: %s %v", edge, got, err)
		}
	}
	if inverse, err := kinds.Inverse(kinds.ComposesWith); err != nil || inverse != "" {
		t.Fatal("invented inverse")
	}
	if _, err := kinds.Inverse("owns"); err == nil {
		t.Fatal("accepted derived inverse as stored edge")
	}
	r := kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.RelatedSkill,
		From:   kinds.Reference{Authority: "registry", Kind: "tool", ID: "tool:cli/git/push"},
		To:     kinds.Reference{Authority: "external", Kind: "skill", ID: "native/opaque:id"},
		Source: "authored", Status: "reviewed",
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := kinds.ParseRelationJSON(raw)
	if err != nil || parsed.To.ID != "native/opaque:id" {
		t.Fatalf("%+v %v", parsed, err)
	}
	r.To.Kind = "agent"
	if err := r.Validate(); err == nil {
		t.Fatal("related_skill accepted non-skill target")
	}
	r.To.Kind = "skill"
	r.Source = "mined"
	if err := r.Validate(); err == nil {
		t.Fatal("mined relation bypassed proposal")
	}
	r.Status = "proposed"
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	w := 1.1
	r.Weight = &w
	if err := r.Validate(); err == nil {
		t.Fatal("weight outside bounds")
	}
	r.Weight = nil
	r.From.Authority = ""
	if err := r.Validate(); err == nil {
		t.Fatal("missing authority")
	}
}
