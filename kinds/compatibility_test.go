package kinds_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKAnnotationsRoundTripAndHash(t *testing.T) {
	v := tool(t)
	annotations := mcp.ToolAnnotations{Title: "Lookup item", ReadOnlyHint: true}
	raw, err := json.Marshal(annotations)
	if err != nil {
		t.Fatal(err)
	}
	v.Definition.Annotations = raw
	wire, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := kinds.ParseToolJSON(wire)
	if err != nil {
		t.Fatal(err)
	}
	var restored mcp.ToolAnnotations
	if err := json.Unmarshal(parsed.Definition.Annotations, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Title != annotations.Title || !restored.ReadOnlyHint {
		t.Fatalf("lost SDK annotations: %+v", restored)
	}
	original, err := kinds.DefinitionHash(v)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := kinds.DefinitionHash(*parsed)
	if err != nil || roundTrip != original {
		t.Fatalf("roundtrip hash %s %v", roundTrip, err)
	}
	parsed.Definition.Title = "Top-level title is outside the projection"
	display, err := kinds.DefinitionHash(*parsed)
	if err != nil || display != original {
		t.Fatalf("top-level title changed hash: %s %v", display, err)
	}
	annotations.Title = "Changed annotation title"
	parsed.Definition.Annotations, err = json.Marshal(annotations)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := kinds.DefinitionHash(*parsed)
	if err != nil || changed == original {
		t.Fatalf("annotations.title did not change hash: %s %v", changed, err)
	}
}

func TestMCPNamesPreserveProtocolGrammar(t *testing.T) {
	for _, name := range []string{"_lookup", "-lookup", ".lookup", ".", "..", strings.Repeat("_", 128)} {
		t.Run(name, func(t *testing.T) {
			v := tool(t)
			id, err := kinds.ToolID("mcp", "example", name)
			if err != nil || id != "tool:mcp/example/"+name {
				t.Fatalf("%q: %s %v", name, id, err)
			}
			v.ID = id
			v.Definition.Name = name
			wire, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			round, err := kinds.ParseToolJSON(wire)
			if err != nil || round.Definition.Name != name || round.ID != id {
				t.Fatalf("roundtrip: %+v %v", round, err)
			}
			if _, err := kinds.DefinitionHash(*round); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"", strings.Repeat("a", 129), "a/b", "white space", "é"} {
		if _, err := kinds.ToolID("mcp", "example", name); err == nil {
			t.Fatalf("accepted invalid MCP name %q", name)
		}
	}
	// Protocol punctuation is not permission to admit CLI flags or paths.
	for _, name := range []string{"git/--force", "../git", "git//push"} {
		if _, err := kinds.ToolID("cli", "", name); err == nil {
			t.Fatalf("accepted invalid CLI signature %q", name)
		}
	}
}

func TestProjectYAMLNumericAdmissionBeforeConversion(t *testing.T) {
	const base = `schema: project/1
id: sample
name: Sample
class: 2
lifecycle: active
visibility: public
owner: user:owner
interfaces: [{type: cli}]
`
	for _, number := range []string{"1e-400", "-1e-400", "1e400", "9007199254740992", "9007199254740993", "-9007199254740993"} {
		t.Run(number, func(t *testing.T) {
			yaml := base + "settings:\n  value: " + number + "\n"
			if p, err := kinds.ParseProjectYAML([]byte(yaml)); err == nil {
				t.Fatalf("YAML accepted lossy number: %#v", p.Settings)
			}
			jsonInput := strings.TrimSuffix(projectJSON, "}") + `,"settings":{"value":` + number + "}}"
			if _, err := kinds.ParseProjectJSON([]byte(jsonInput)); err == nil {
				t.Fatal("JSON accepted lossy number")
			}
		})
	}
	for _, input := range []string{
		"settings:\n  nested: [{value: 1e-400}]\n",
		"settings:\n  value: &tiny 1e-400\n  copy: *tiny\n",
		"settings:\n  value: .inf\n",
		"settings:\n  value: .nan\n",
		"settings:\n  value: 0x20000000000001\n",
	} {
		if _, err := kinds.ParseProjectYAML([]byte(base + input)); err == nil {
			t.Fatalf("accepted lossy YAML %q", input)
		}
	}
	for _, number := range []string{"0", "-0", "0.0", "0e-400", "5e-324", "0.1", "9007199254740991", "-9007199254740991", "+.5", "01.5", "09", "0x10", "0o10", "0b10", "1_000.25"} {
		t.Run("valid_"+number, func(t *testing.T) {
			p, err := kinds.ParseProjectYAML([]byte(base + "settings:\n  value: " + number + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := kinds.ParseProjectJSON(raw); err != nil {
				t.Fatalf("JSON roundtrip: %v", err)
			}
			if number == "5e-324" && p.Settings["value"] == float64(0) {
				t.Fatal("smallest admitted value became zero")
			}
		})
	}
	for _, value := range []string{`"1e-400"`, `!!str 1e-400`, `"1e400"`} {
		p, err := kinds.ParseProjectYAML([]byte(base + "settings:\n  value: " + value + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := p.Settings["value"].(string); !ok {
			t.Fatalf("intentional string converted: %#v", p.Settings)
		}
	}
}

func TestProjectParsingDoesNotCertifyClassBar(t *testing.T) {
	for _, descriptor := range []string{
		projectJSON, // A class 2 CLI may honestly declare auth:none.
		strings.Replace(projectJSON, `"class":2`, `"class":1`, 1), // no operator or enforce
		strings.Replace(strings.Replace(projectJSON, `"class":2`, `"class":1`, 1), `"auth":"none"`, `"auth":"loopback_open"`, 1),
	} {
		p, err := kinds.ParseProjectJSON([]byte(descriptor))
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, err := p.CanonicalID(); err != nil {
			t.Fatal(err)
		}
	}
}
