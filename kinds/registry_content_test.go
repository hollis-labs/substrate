package kinds_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func registryExamples() map[string]string {
	return map[string]string{
		kinds.ServiceSchema:       `"owner":"example-owner","operator":"example-operator","host":"example-host","cerberus_resource_id":"catalog","deploy_method":"cerberus","description":"Provides catalog lookup","project":{"authority":"registry","kind":"project","id":"project:example"},"interfaces":[{"type":"mcp","name":"catalog"}]`,
		kinds.ConfigSchema:        `"scope":"service","target":{"authority":"registry","kind":"service","id":"service:example"},"settings_schema":{"type":"object","properties":{"limit":{"type":"integer","maximum":10}},"additionalProperties":false},"settings":{"limit":5},"secret_refs":{"API_KEY":{"authority":"vault","kind":"secret","id":"catalog/api-key"}}`,
		kinds.KnowledgeSchema:     `"format":"markdown","body":"# Guide\nKeep exact text.\n","provenance":[{"authority":"author","kind":"document","id":"native/../guide"}],"upstream":{"reference":{"authority":"tesseract","kind":"knowledge","id":"native/item"},"revision":"v/1"}`,
		kinds.MCPServerSchema:     `"transport":"stdio","source":{"reference":{"authority":"tether","kind":"mcp_server","id":"server/native"},"revision":"v/1"}`,
		kinds.LaunchProfileSchema: `"source":{"reference":{"authority":"tether","kind":"launch_profile","id":"profile/native"},"revision":"v/1"}`,
	}
}

func registryWire(schema, fields string) []byte {
	return []byte(`{"schema":"` + schema + `","id":"Native/../id","name":"Example",` + fields + `}`)
}

func TestRegistryContentContracts(t *testing.T) {
	for schema, fields := range registryExamples() {
		t.Run(schema, func(t *testing.T) {
			raw := registryWire(schema, fields)
			out, err := kinds.ParseContentJSON(schema, raw)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if err = kinds.ValidateJSON(schema, encoded); err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err = json.Unmarshal(raw, &before); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			beforeBytes, _ := json.Marshal(before)
			afterBytes, _ := json.Marshal(after)
			if string(beforeBytes) != string(afterBytes) {
				t.Fatalf("roundtrip changed payload: %s", encoded)
			}
			id, err := kinds.CanonicalContentID(schema, "Native/../id")
			if err != nil || id != strings.TrimSuffix(schema, "/1")+":Native/../id" {
				t.Fatalf("identity: %s %v", id, err)
			}
			for _, extra := range []string{`"permissions":{"network":true}`, `"published":true`, `"session_id":"runtime"`} {
				if _, err = kinds.ParseContentJSON(schema, registryWire(schema, fields+","+extra)); err == nil {
					t.Fatalf("accepted %s", extra)
				}
			}
		})
	}
}

func TestRegistryContentRejectsWrongReferencesAndShapes(t *testing.T) {
	examples := registryExamples()
	tests := []struct{ schema, old, replacement string }{
		{kinds.ServiceSchema, `"host":"example-host"`, `"host":""`},
		{kinds.ServiceSchema, `"kind":"project"`, `"kind":"agent"`},
		{kinds.ServiceSchema, `"type":"mcp"`, `"type":"invalid"`},
		{kinds.ServiceSchema, `"deploy_method":"cerberus"`, `"deploy_method":"unknown"`},
		{kinds.ConfigSchema, `"scope":"service"`, `"scope":"app"`},
		{kinds.ConfigSchema, `"kind":"secret"`, `"kind":"document"`},
		{kinds.KnowledgeSchema, `"body":"# Guide\nKeep exact text.\n"`, `"body":""`},
		{kinds.KnowledgeSchema, `"format":"markdown"`, `"format":"html"`},
		{kinds.KnowledgeSchema, `"authority":"tesseract"`, `"authority":"registry"`},
		{kinds.MCPServerSchema, `"revision":"v/1"`, `"revision":""`},
		{kinds.MCPServerSchema, `"transport":"stdio"`, `"transport":"ftp"`},
		{kinds.LaunchProfileSchema, `"authority":"tether"`, `"authority":"other"`},
		{kinds.LaunchProfileSchema, `"kind":"launch_profile"`, `"kind":"agent"`},
	}
	for _, tc := range tests {
		fields := strings.Replace(examples[tc.schema], tc.old, tc.replacement, 1)
		if fields == examples[tc.schema] {
			t.Fatalf("invalid test replacement: %s", tc.old)
		}
		if err := kinds.ValidateJSON(tc.schema, registryWire(tc.schema, fields)); err == nil {
			t.Fatalf("accepted %s replacement %s", tc.schema, tc.replacement)
		}
	}
	// Every required field is independently refused when omitted from valid input.
	for schema, fields := range examples {
		var document map[string]any
		if err := json.Unmarshal(registryWire(schema, fields), &document); err != nil {
			t.Fatal(err)
		}
		schemaBytes, err := kinds.Schema(schema)
		if err != nil {
			t.Fatal(err)
		}
		var contract struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(schemaBytes, &contract); err != nil {
			t.Fatal(err)
		}
		for _, key := range contract.Required {
			value := document[key]
			delete(document, key)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := kinds.ValidateJSON(schema, raw); err == nil {
				t.Fatalf("%s accepted missing %s", schema, key)
			}
			document[key] = value
		}
	}
	for _, port := range []string{"0", "65536", "1.5"} {
		if err := kinds.ValidateJSON(kinds.ServiceSchema, registryWire(kinds.ServiceSchema, examples[kinds.ServiceSchema]+`,"port":`+port)); err == nil {
			t.Fatalf("accepted port %s", port)
		}
	}
}

func TestRegistryContentRefusesSecretsBeforeSchemaErrors(t *testing.T) {
	// Deliberately synthetic strings exercise credential syntax, never real keys.
	credential := "Bearer " + strings.Repeat("a", 32)
	for schema, fields := range registryExamples() {
		t.Run(schema, func(t *testing.T) {
			for _, extra := range []string{
				`"metadata":{"nested":[{"apiKey":"example"}]}`,
				`"metadata":{"nested":{"PASSWORD":"placeholder"}}`,
				`"unexpected":"` + credential + `"`,
				`"metadata":{"note":"` + credential + `"}`,
			} {
				raw := registryWire(schema, fields+","+extra)
				for _, parse := range []func() error{
					func() error { return kinds.ValidateJSON(schema, raw) },
					func() error { _, err := kinds.ParseContentJSON(schema, raw); return err },
				} {
					err := parse()
					if !errors.Is(err, kinds.ErrSecretValue) {
						t.Fatalf("expected secret refusal, got %v", err)
					}
					if strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), "synthetic-value") {
						t.Fatal("error disclosed rejected value")
					}
				}
			}
		})
	}
}

func TestSecretAdmissionReferencesAndDecodedText(t *testing.T) {
	for _, raw := range []string{
		`{"API_KEY":{"authority":"vault","kind":"secret","id":"example/key"}}`,
		`{"body":"Use secretref://example/key, never inline credentials."}`,
		`{"settings":{"token_limit":200,"tokenizer":"example","secret_name":"example"}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"api\u004bey":"synthetic-value"}`,
		`{"apiKey":{"authority":"vault","kind":"secret","id":"example/key","value":"hidden"}}`,
		`{"settings":{"nested":[{"client_secret":"literal"}]}}`,
		`{"body":"password=synthetic-value"}`,
		`{"metadata":{"Authorization":["opaque"]}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); !errors.Is(err, kinds.ErrSecretValue) {
			t.Fatalf("expected refusal, got %v", err)
		}
	}
}

func TestConfigSchemaAndScopeValidation(t *testing.T) {
	base := registryExamples()[kinds.ConfigSchema]
	for _, fields := range []string{
		strings.Replace(base, `"limit":5`, `"limit":11`, 1),
		strings.Replace(base, `"scope":"service"`, `"scope":"agent"`, 1),
		strings.Replace(base, `"type":"integer"`, `"type":"not-a-type"`, 1),
		strings.Replace(base, `"type":"integer"`, `"$ref":"https://example.invalid/schema"`, 1),
	} {
		if err := kinds.ValidateJSON(kinds.ConfigSchema, registryWire(kinds.ConfigSchema, fields)); err == nil {
			t.Fatal("accepted invalid config settings/schema/scope")
		}
	}
	fields := strings.Replace(base, `"type":"integer","maximum":10`, `"$ref":"#/$defs/limit"`, 1)
	fields = strings.Replace(fields, `"additionalProperties":false`, `"additionalProperties":false,"$defs":{"limit":{"type":"integer","maximum":10}}`, 1)
	if err := kinds.ValidateJSON(kinds.ConfigSchema, registryWire(kinds.ConfigSchema, fields)); err != nil {
		t.Fatalf("local schema bundle: %v", err)
	}
}

func TestRegistryEdges(t *testing.T) {
	for _, tc := range []struct {
		edge     kinds.EdgeType
		from, to string
	}{
		{kinds.DeployedAs, "project", "service"},
		{kinds.Configures, "config", "project"},
		{kinds.Configures, "config", "service"},
		{kinds.Configures, "config", "agent"},
		{kinds.Configures, "config", "agency"},
		{kinds.PartOf, "service", "project"},
		{kinds.Consumes, "mcp_server", "config"},
		{kinds.Consumes, "agent", "launch_profile"},
		{kinds.PartOf, "knowledge", "project"},
	} {
		r := kinds.Relation{Schema: kinds.RelationSchema, Type: tc.edge,
			From: kinds.Reference{Authority: "registry", Kind: tc.from, ID: tc.from + ":one"},
			To:   kinds.Reference{Authority: "registry", Kind: tc.to, ID: tc.to + ":two"}, Source: "authored", Status: "proposed"}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if tc.edge == kinds.DeployedAs || tc.edge == kinds.Configures {
			inverse, err := kinds.Inverse(tc.edge)
			if err != nil || inverse != "" {
				t.Fatalf("invented inverse: %q %v", inverse, err)
			}
			r.To.Kind = "tool"
			if err := r.Validate(); err == nil {
				t.Fatal("accepted invalid edge target")
			}
			r.To.Kind = tc.to
			r.From.Kind = "tool"
			if err := r.Validate(); err == nil {
				t.Fatal("accepted invalid edge source")
			}
		}
	}
}
