package kinds_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestSchemaSecretReferenceProperties(t *testing.T) {
	schema := `{"type":"object","properties":{"apiKey":{"type":"object","properties":{"authority":{"type":"string"},"kind":{"const":"secret"},"id":{"type":"string"}},"required":["authority","kind","id"],"additionalProperties":false}},"additionalProperties":false}`
	settings := `{"apiKey":{"authority":"vault","kind":"secret","id":"example/key"}}`
	config := registryWire(kinds.ConfigSchema, `"scope":"service","target":{"authority":"registry","kind":"service","id":"service:example"},"settings_schema":`+schema+`,"settings":`+settings)
	if err := kinds.ValidateJSON(kinds.ConfigSchema, config); err != nil {
		t.Fatal(err)
	}
	// The same declared schema is admitted in supported tool/skill schema slots
	// and Registry's full original Input envelope, with no credential exemption
	// for the enclosing metadata/source/summary or instance payload.
	for _, raw := range []string{
		`{"schema":"tool/1","definition":{"input_schema":` + schema + `}}`,
		`{"schema":"skill/1","output_schema":` + schema + `}`,
		`{"schema":"config/1","payload":` + string(config) + `}`,
		`{"schema":"tool/1","payload":{"definition":{"input_schema":` + schema + `}}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSchemaLiteralSecretsStillRefused(t *testing.T) {
	for _, literal := range []string{
		`"default":"example"`,
		`"const":"example"`,
		`"enum":["example"]`,
		`"examples":["example"]`,
		`"allOf":[{"default":"example"}]`,
	} {
		raw := `{"schema":"tool/1","payload":{"definition":{"input_schema":{"type":"object","properties":{"apiKey":{"type":"string",` + literal + `}}}}}}`
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); !errors.Is(err, kinds.ErrSecretValue) {
			t.Fatalf("literal %s: %v", literal, err)
		}
	}
	for _, schema := range []string{
		`{"default":{"apiKey":"example"}}`,
		`{"const":{"apiKey":"example"}}`,
		`{"examples":[{"apiKey":"example"}]}`,
		`{"description":"Bearer ` + strings.Repeat("a", 32) + `"}`,
	} {
		raw := `{"schema":"tool/1","definition":{"input_schema":` + schema + `}}`
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); !errors.Is(err, kinds.ErrSecretValue) {
			t.Fatalf("expected schema literal refusal, got %v", err)
		}
	}
	// A property named input_schema inside metadata is instance data, not an
	// admission escape hatch. Pointer-like keys must not spoof structural paths.
	for _, raw := range []string{
		`{"schema":"tool/1","metadata":{"input_schema":{"properties":{"apiKey":"example"}}}}`,
		`{"schema":"tool/1","definition/input_schema":{"properties":{"apiKey":"example"}}}`,
		`{"schema":"config/1","settings":{"settings_schema":{"properties":{"apiKey":"example"}}}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); !errors.Is(err, kinds.ErrSecretValue) {
			t.Fatalf("instance bypass: %v", err)
		}
	}
	reference := map[string]any{"authority": "vault", "kind": "secret", "id": "example/key"}
	for _, keyword := range []string{"default", "const"} {
		raw, err := json.Marshal(map[string]any{"schema": "tool/1", "definition": map[string]any{"input_schema": map[string]any{"properties": map[string]any{"apiKey": map[string]any{keyword: reference}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = kinds.ValidateNoSecretsJSON(raw); err != nil {
			t.Fatal(err)
		}
	}
}
