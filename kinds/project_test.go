package kinds_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

const projectJSON = `{"schema":"project/1","id":"sample","name":"Sample","class":2,"lifecycle":"active","visibility":"public","owner":"user:owner","interfaces":[{"type":"cli","name":"sample","auth":"none"}]}`

func TestProjectDescriptor(t *testing.T) {
	t.Parallel()
	p, err := kinds.ParseProjectJSON([]byte(projectJSON))
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.CanonicalID()
	if err != nil || id != "project:sample" || p.ID != "sample" {
		t.Fatalf("identity: %q, %v", id, err)
	}
	p.Settings = map[string]any{"runtime": map[string]any{"mode": "dev"}}
	p.Edges = []kinds.ProjectEdge{{Type: kinds.Consumes, To: "tool:cli/git/push"}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = kinds.ParseProjectJSON(raw); err != nil {
		t.Fatal(err)
	}
}

func TestProjectValidation(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, old, new string }{
		{"unknown key", `"class":2`, `"class":2,"clas":2`},
		{"verification stamp", `"class":2`, `"class":2,"last_verified":"now"`},
		{"bad version", "project/1", "project/2"},
		{"missing name", `"name":"Sample",`, ""},
		{"blank name", `"name":"Sample"`, `"name":""`},
		{"string class", `"class":2`, `"class":"2"`},
		{"unknown class", `"class":2`, `"class":4`},
		{"bad slug", `"id":"sample"`, `"id":"sample/other"`},
		{"bad owner", "user:owner", "owner"},
		{"empty interfaces", `[{"type":"cli","name":"sample","auth":"none"}]`, `[]`},
		{"unknown interface", "type\":\"cli", "type\":\"terminal"},
		{"unknown auth", "auth\":\"none", "auth\":\"open"},
		{"unknown nested field", `"auth":"none"`, `"auth":"none","secret":"x"`},
		{"library with cli", `"class":2`, `"class":"lib"`},
		{"null settings", `"class":2`, `"class":2,"settings":null`},
		{"duplicate key", `"class":2`, `"class":2,"class":1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := strings.Replace(projectJSON, tt.old, tt.new, 1)
			if data == projectJSON {
				t.Fatal("test did not modify input")
			}
			if _, err := kinds.ParseProjectJSON([]byte(data)); err == nil {
				t.Fatal("invalid project accepted")
			}
		})
	}
	lib := strings.ReplaceAll(strings.Replace(projectJSON, `"class":2`, `"class":"lib"`, 1), `"type":"cli"`, `"type":"lib"`)
	if _, err := kinds.ParseProjectJSON([]byte(lib)); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"2.0", "2e0"} {
		if _, err := kinds.ParseProjectJSON([]byte(strings.Replace(projectJSON, `"class":2`, `"class":`+n, 1))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectYAML(t *testing.T) {
	t.Parallel()
	data := `schema: project/1
id: sample
name: Sample
class: 2
lifecycle: active
visibility: public
owner: user:owner
interfaces:
  - type: cli
    name: sample
    auth: none
settings:
  runtime:
    port: 8080
`
	p, err := kinds.ParseProjectYAML([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if p.Class != kinds.ClassTool {
		t.Fatal(p.Class)
	}
	for _, bad := range []string{data + "---\nname: second\n", data + "id: duplicate\n", data + "rogue: true\n", strings.Replace(data, "class: 2", "class: '2'", 1)} {
		if _, err := kinds.ParseProjectYAML([]byte(bad)); err == nil {
			t.Fatal("invalid YAML accepted")
		}
	}
}

func TestSchemaDiscovery(t *testing.T) {
	t.Parallel()
	for _, name := range kinds.Schemas() {
		raw, err := kinds.Schema(name)
		if err != nil || !json.Valid(raw) {
			t.Fatalf("%s: %v", name, err)
		}
		raw[0] = 'x'
		again, err := kinds.Schema(name)
		if err != nil || !json.Valid(again) {
			t.Fatal("caller mutated shared schema")
		}
	}
	if _, err := kinds.Schema("project/99"); !errors.Is(err, kinds.ErrUnknownSchema) {
		t.Fatal(err)
	}
	if err := kinds.ValidateJSON("project/99", []byte("{}")); !errors.Is(err, kinds.ErrUnknownSchema) {
		t.Fatal(err)
	}
}
