package kinds_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

const toolJSON = `{"schema":"tool/1","id":"tool:mcp/example/lookup","source":"mcp","origin":"example","lifecycle":"active","definition":{"name":"lookup","input_schema":{"type":"object"}}}`

func tool(t *testing.T) kinds.Tool {
	t.Helper()
	v, err := kinds.ParseToolJSON([]byte(toolJSON))
	if err != nil {
		t.Fatal(err)
	}
	return *v
}

func TestToolIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ source, origin, name, want string }{
		{"mcp", "example", "lookup", "tool:mcp/example/lookup"},
		{"builtin", "claude", "Bash", "tool:builtin/claude/Bash"},
		{"cli", "", "docker/compose/up", "tool:cli/docker/compose/up"},
	} {
		got, err := kinds.ToolID(tc.source, tc.origin, tc.name)
		if err != nil || got != tc.want {
			t.Fatalf("%s %v", got, err)
		}
	}
	for _, name := range []string{"/usr/bin/git", "git push", "git//push", "git/--force", "git/../push", "git/"} {
		if _, err := kinds.ToolID("cli", "", name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if _, err := kinds.ToolID("cli", "host", "git/push"); err == nil {
		t.Fatal("CLI authority became identity namespace")
	}
	a := tool(t)
	a.ID = "tool:mcp/other/lookup"
	if err := a.Validate(); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	raw, _ := json.Marshal(a)
	if err := kinds.ValidateJSON(kinds.ToolSchema, raw); err == nil {
		t.Fatal("generic validator missed identity mismatch")
	}
}

func TestToolValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, old, new string }{
		{"review bypass", `"lifecycle":"active"`, `"lifecycle":"active","effective":{"approval_tier":"ground"}`},
		{"reviewer bypass", `"lifecycle":"active"`, `"lifecycle":"active","reviewer":"owner"`},
		{"missing input", `,"input_schema":{"type":"object"}`, ""},
		{"null input", `"input_schema":{"type":"object"}`, `"input_schema":null`},
		{"wrong input type", `"input_schema":{"type":"object"}`, `"input_schema":{"type":"string"}`},
		{"unknown hint", `"name":"lookup"`, `"name":"lookup","annotations":{"read_only":true}`},
		{"wrong hint type", `"name":"lookup"`, `"name":"lookup","annotations":{"readOnlyHint":"true"}`},
		{"unknown definition field", `"name":"lookup"`, `"name":"lookup","reviewed":true`},
		{"duplicate alias", `"lifecycle":"active"`, `"lifecycle":"active","aliases":[{"system":"cli","alias":"a"},{"system":"cli","alias":"a"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := kinds.ParseToolJSON([]byte(strings.Replace(toolJSON, tc.old, tc.new, 1))); err == nil {
				t.Fatal("invalid tool accepted")
			}
		})
	}
}

func TestDefinitionGoldenVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		edit            func(*kinds.Tool)
		canonical, hash string
	}{
		{"absent", func(*kinds.Tool) {},
			`{"input_schema":{"type":"object"},"name":"lookup"}`,
			"353b097682c5ff2a8cfe220300656eb3512d065efc531043fff8a285b6c75a68"},
		{"null", func(v *kinds.Tool) { v.Definition.Description = json.RawMessage("null") },
			`{"description":null,"input_schema":{"type":"object"},"name":"lookup"}`,
			"ffcbdbe051320560f19503ba4a57b63d035f210f2e084eea7af8dce6dc1fd669"},
		{"number and escape", func(v *kinds.Tool) {
			v.Definition.Description = json.RawMessage(`"Find <item>"`)
			v.Definition.Annotations = json.RawMessage(`{"readOnlyHint":true}`)
			v.Definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","default":2e-3}}}`)
		},
			`{"annotations":{"readOnlyHint":true},"description":"Find <item>","input_schema":{"properties":{"n":{"default":0.002,"type":"number"}},"type":"object"},"name":"lookup"}`,
			"c9eb7c666d58308e2d822cdc063703016553856c1492842143afea758034bc3c"},
		{"CLI grammar", func(v *kinds.Tool) {
			v.ID = "tool:cli/git/push"
			v.Source = "cli"
			v.Origin = ""
			v.Definition = kinds.Definition{Name: "git push", CLI: &kinds.CLIGrammar{
				Signature: "git/push", HelpTextDigest: strings.Repeat("0", 64), KnownFlags: []string{"--force", "--dry-run"},
			}}
		},
			`{"help_text_digest":"0000000000000000000000000000000000000000000000000000000000000000","known_flags":["--force","--dry-run"],"signature":"git/push"}`,
			"98179984e3e4344f1c19008e6a7e702578f04e50ecbd62a235aa9f5b897d747d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tool(t)
			tt.edit(&v)
			projection, err := kinds.DefinitionProjection(v)
			if err != nil || string(projection) != tt.canonical {
				t.Fatalf("projection %s, %v; want %s", projection, err, tt.canonical)
			}
			hash, err := kinds.DefinitionHash(v)
			if err != nil || hash != tt.hash {
				t.Fatalf("hash %s, %v; want %s", hash, err, tt.hash)
			}
		})
	}
}

func TestHashProjectionAndPresence(t *testing.T) {
	t.Parallel()
	v := tool(t)
	base, err := kinds.DefinitionHash(v)
	if err != nil {
		t.Fatal(err)
	}
	v.Definition.Title = "A display-only title"
	v.Definition.Icons = json.RawMessage(`[{"src":"https://example.com/icon.png"}]`)
	v.Definition.Meta = json.RawMessage(`{"display":"compact"}`)
	v.Definition.SourceRef = "source:revision-2"
	v.Owner = "user:other"
	v.Aliases = []kinds.Alias{{System: "harness", Alias: "lookup_alias"}}
	h, err := kinds.DefinitionHash(v)
	if err != nil || h != base {
		t.Fatalf("nonprojected fields changed hash: %s %v", h, err)
	}
	seen := map[string]bool{base: true}
	for _, annotations := range []string{"null", "{}", `{"readOnlyHint":null}`, `{"readOnlyHint":false}`, `{"readOnlyHint":true}`} {
		v.Definition.Annotations = json.RawMessage(annotations)
		hash, err := kinds.DefinitionHash(v)
		if err != nil {
			t.Fatal(err)
		}
		if seen[hash] {
			t.Fatalf("collapsed annotation presence/value %s", annotations)
		}
		seen[hash] = true
	}
	v = tool(t)
	v.Definition.OutputSchema = json.RawMessage("null")
	h, err = kinds.DefinitionHash(v)
	if err != nil || h == base {
		t.Fatal("output schema null collapsed with absence")
	}
	v = tool(t)
	v.Definition.InputSchema = json.RawMessage(`{"properties":{"b":{},"a":{}},"type":"object"}`)
	a, err := kinds.DefinitionHash(v)
	if err != nil {
		t.Fatal(err)
	}
	v.Definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"a":{},"b":{}}}`)
	b, err := kinds.DefinitionHash(v)
	if err != nil || a != b {
		t.Fatal("object key order changed hash")
	}
}

func TestHashRejectsAmbiguousJSON(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		`{"type":"object","type":"object"}`,
		`{"type":"object","default":"\ud800"}`,
		`{"type":"object","default":"\udc00"}`,
		`{"type":"object","default":9007199254740992}`,
		`{"type":"object","default":9007199254740993}`,
		`{"type":"object","default":1e400}`,
		`{"type":"object","default":1e-400}`,
		"{\"type\":\"object\",\"default\":\"\xff\"}",
	} {
		v := tool(t)
		v.Definition.InputSchema = json.RawMessage(s)
		if _, err := kinds.DefinitionHash(v); err == nil {
			t.Fatalf("accepted ambiguous input %q", s)
		}
	}
	for _, number := range []string{"9007199254740991", "-9007199254740991", "5e-324", "0.1", "-0"} {
		v := tool(t)
		v.Definition.InputSchema = json.RawMessage(`{"type":"object","default":` + number + "}")
		if _, err := kinds.DefinitionHash(v); err != nil {
			t.Fatalf("%s: %v", number, err)
		}
	}
}

func TestClassificationValue(t *testing.T) {
	t.Parallel()
	read := kinds.Classification{SideEffect: []string{"read"}, ApprovalTier: "ground"}
	if err := read.Validate(); err != nil {
		t.Fatal(err)
	}
	read.ApprovalTier = "director"
	if err := read.Validate(); err == nil {
		t.Fatal("accepted clearance role as TR-1 approval tier")
	}
	read.ApprovalTier = "ground"
	read.SideEffect = []string{"external-comms"}
	if err := read.Validate(); err == nil {
		t.Fatal("accepted unknown side-effect spelling")
	}
}

func TestJCSUnicodeAndNumericOrdering(t *testing.T) {
	t.Parallel()
	v := tool(t)
	v.Definition.InputSchema = json.RawMessage(`{"type":"object","default":{"\ue000":1,"\ud800\udc00":2}}`)
	got, err := kinds.DefinitionProjection(v)
	want := "{\"input_schema\":{\"default\":{\"𐀀\":2,\"\":1},\"type\":\"object\"},\"name\":\"lookup\"}"
	if err != nil || string(got) != want {
		t.Fatalf("UTF-16 key ordering: %s %v", got, err)
	}
	for _, pair := range [][2]string{{"0", "-0"}, {"0.002", "2e-3"}, {"1", "1.0"}} {
		v.Definition.InputSchema = json.RawMessage(`{"type":"object","default":` + pair[0] + "}")
		a, err := kinds.DefinitionHash(v)
		if err != nil {
			t.Fatal(err)
		}
		v.Definition.InputSchema = json.RawMessage(`{"type":"object","default":` + pair[1] + "}")
		b, err := kinds.DefinitionHash(v)
		if err != nil || a != b {
			t.Fatalf("equivalent numbers %v: %s %s %v", pair, a, b, err)
		}
	}
}
