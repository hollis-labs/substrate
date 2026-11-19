package kinds_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestSemanticReferences(t *testing.T) {
	examples := registryExamples()
	ref := func(kind, id string) kinds.Reference {
		return kinds.Reference{Authority: "registry", Kind: kind, ID: id}
	}
	config := ref("config", "config:shared")
	tool := ref("tool", "tool:example")
	for schema, fields := range examples {
		var payload map[string]any
		if err := json.Unmarshal(registryWire(schema, fields), &payload); err != nil {
			t.Fatal(err)
		}
		// Non-semantic source/descriptive/secret/provenance refs in the examples must
		// not leak into edges. A descriptive Registry ref is excluded as well.
		payload["references"] = []kinds.Reference{ref("project", "project:descriptive")}
		want := []kinds.SemanticReference{}
		switch schema {
		case kinds.ServiceSchema:
			payload["config"] = []kinds.Reference{config, config}
			want = []kinds.SemanticReference{{Relation: kinds.Consumes, Reference: config}, {Relation: kinds.PartOf, Reference: ref("project", "project:example")}}
		case kinds.ConfigSchema:
			want = []kinds.SemanticReference{{Relation: kinds.Configures, Reference: ref("service", "service:example")}}
		case kinds.MCPServerSchema:
			payload["tools"] = []kinds.Reference{tool, tool}
			payload["config"] = []kinds.Reference{config}
			want = []kinds.SemanticReference{{Relation: kinds.Consumes, Reference: config}, {Relation: kinds.Consumes, Reference: tool}}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := kinds.SemanticReferences(schema, raw)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v want %+v error %v", schema, got, want, err)
		}
		// Invalid kind payloads cannot produce apparently admitted dependencies.
		delete(payload, "id")
		raw, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = kinds.SemanticReferences(schema, raw); err == nil {
			t.Fatal("extracted invalid payload")
		}
	}
	if _, err := kinds.SemanticReferences(kinds.ToolSchema, []byte(`{}`)); err == nil {
		t.Fatal("accepted unsupported kind")
	}
}
