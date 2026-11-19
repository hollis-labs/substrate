package kinds_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestContentDefinitionsPreserveBodiesAndReferences(t *testing.T) {
	cases := map[string]string{
		kinds.AgentSchema:       `{"charter":"Read the supplied brief.\nReport evidence.","roles":["reviewer"],"launch_profile":{"reference":{"authority":"tether","kind":"launch_profile","id":"profile/native/v2"},"revision":"rev/2"}}`,
		kinds.AgencySchema:      `{"framework":"citadel","charter":"Review changes","cells":[{"name":"reviewer","definition":{"charter":"Read and assess","roles":["reviewer"]}},{"name":"writer","reference":{"authority":"registry","kind":"agent","id":"agent:writer"}}]}`,
		kinds.SkillSchema:       `{"instructions":"# Inspect\nKeep this body.","tools":[{"authority":"registry","kind":"tool","id":"tool:mcp/example/.."}]}`,
		kinds.DocumentSchema:    `{"subtype":"engineering_bible","format":"markdown","body":"# Rules\n\nKeep exact content.\n"}`,
		kinds.WorkstreamSchema:  `{"status":"active","workflow_id":"workflow/native/1","project_urns":["urn:project:test"],"source_created_at":"source-timestamp"}`,
		kinds.CorrelationSchema: `{"links":{"task":{"authority":"torque","kind":"task","id":"CW-opaque/1"}},"source_ids":{"correlation_id":"one","in_reply_to":"two","causation_id":"three"},"trace":{"depth":0}}`,
	}
	for schema, body := range cases {
		t.Run(schema, func(t *testing.T) {
			raw := `{"schema":"` + schema + `","id":"native/../_id","name":"Example",` + body[1:]
			parsed, err := kinds.ParseContentJSON(schema, []byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			if err = json.Unmarshal([]byte(raw), &a); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(out, &b); err != nil {
				t.Fatal(err)
			}
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if string(aa) != string(bb) {
				t.Fatalf("roundtrip changed body/reference: %s", out)
			}
			id, err := kinds.CanonicalContentID(schema, "native/../_id")
			if err != nil || id != strings.TrimSuffix(schema, "/1")+":native/../_id" {
				t.Fatalf("identity: %q %v", id, err)
			}
		})
	}
}

func TestContentRejectsEmptyDefinitionsAndRuntimeFields(t *testing.T) {
	cases := []struct{ schema, tail string }{
		{kinds.DocumentSchema, `"subtype":"unknown","format":"markdown","body":"text"`},
		{kinds.DocumentSchema, `"subtype":"sop","format":"markdown","body":""`},
		{kinds.AgentSchema, `"charter":"a","roles":[],"session_id":"runtime"`},
		{kinds.AgentSchema, `"charter":"a","roles":["writer"],"launch_profile":{"reference":{"authority":"tether","kind":"profile","id":"p"}}`},
		{kinds.AgencySchema, `"framework":"citadel","charter":"a","cells":[{"name":"empty"}]`},
		{kinds.AgencySchema, `"framework":"citadel","charter":"a","cells":[{"name":"both","definition":{"charter":"a","roles":["r"]},"reference":{"authority":"registry","kind":"agent","id":"a"}}]`},
		{kinds.CorrelationSchema, `"links":{}`},
		{kinds.CorrelationSchema, `"links":{"task":{"authority":"torque","kind":"task","id":"t"}},"trace":{"depth":-1}`},
		{kinds.WorkstreamSchema, `"status":"active","lease_token":"secret"`},
	}
	for _, tc := range cases {
		raw := `{"schema":"` + tc.schema + `","id":"example","name":"Example",` + tc.tail + `}`
		if _, err := kinds.ParseContentJSON(tc.schema, []byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestCorrelationIntegerSpellingsAgreeWithValidation(t *testing.T) {
	for _, n := range []string{"1", "1.0", "1e0", "0.0", "9007199254740991"} {
		raw := []byte(`{"schema":"correlation/1","id":"c","name":"C","links":{"task":{"authority":"torque","kind":"task","id":"t"}},"trace":{"depth":` + n + `}}`)
		if err := kinds.ValidateJSON(kinds.CorrelationSchema, raw); err != nil {
			t.Fatal(err)
		}
		out, err := kinds.ParseContentJSON(kinds.CorrelationSchema, raw)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if out.(*kinds.Correlation).Trace.Depth == nil {
			t.Fatal("depth lost")
		}
	}
	for _, n := range []string{"1.5", "-1", "9007199254740992", "1e400", "1e-400"} {
		raw := []byte(`{"schema":"correlation/1","id":"c","name":"C","links":{"task":{"authority":"torque","kind":"task","id":"t"}},"trace":{"depth":` + n + `}}`)
		if err := kinds.ValidateJSON(kinds.CorrelationSchema, raw); err == nil {
			t.Errorf("validate accepted %s", n)
		}
		if _, err := kinds.ParseContentJSON(kinds.CorrelationSchema, raw); err == nil {
			t.Errorf("parse accepted %s", n)
		}
	}
}
func TestSpecializedReferencesRejectWrongKinds(t *testing.T) {
	cases := []struct{ schema, tail string }{
		{kinds.AgentSchema, `"charter":"a","roles":["r"],"launch_profile":{"reference":{"authority":"tether","kind":"document","id":"p"},"revision":"r"}`},
		{kinds.AgentSchema, `"charter":"a","roles":["r"],"tools":[{"authority":"registry","kind":"project","id":"p"}]`},
		{kinds.AgentSchema, `"charter":"a","roles":["r"],"skills":[{"authority":"registry","kind":"tool","id":"p"}]`},
		{kinds.SkillSchema, `"instructions":"a","documents":[{"authority":"registry","kind":"agent","id":"p"}]`},
		{kinds.AgencySchema, `"framework":"citadel","charter":"a","cells":[{"name":"r","reference":{"authority":"registry","kind":"tool","id":"p"}}]`},
	}
	for _, tc := range cases {
		raw := []byte(`{"schema":"` + tc.schema + `","id":"example","name":"Example",` + tc.tail + `}`)
		if err := kinds.ValidateJSON(tc.schema, raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	// Parent/causation can point to events or messages; inventory supplies no
	// universal concrete kind. Native authority/ID remain unchanged.
	raw := []byte(`{"schema":"correlation/1","id":"c","name":"C","links":{"task":{"authority":"external","kind":"work_item","id":"native-task"},"parent":{"authority":"external","kind":"message","id":"native/../id"},"thread":{"authority":"external","kind":"conversation","id":"thread/1"}}}`)
	out, err := kinds.ParseContentJSON(kinds.CorrelationSchema, raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.(*kinds.Correlation).Links["parent"].ID != "native/../id" {
		t.Fatal("rewrote native ID")
	}
}

func TestWorkstreamRetainsEmptySourceName(t *testing.T) {
	for _, name := range []string{"", `,"name":""`} {
		raw := []byte(`{"schema":"workstream/1","id":"native-id","status":"active"` + name + `}`)
		if err := kinds.ValidateJSON(kinds.WorkstreamSchema, raw); err != nil {
			t.Fatal(err)
		}
		out, err := kinds.ParseContentJSON(kinds.WorkstreamSchema, raw)
		if err != nil {
			t.Fatal(err)
		}
		if out.(*kinds.Workstream).Name != "" {
			t.Fatal("invented source name")
		}
		wire, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := kinds.ValidateJSON(kinds.WorkstreamSchema, wire); err != nil {
			t.Fatal(err)
		}
	}
}
