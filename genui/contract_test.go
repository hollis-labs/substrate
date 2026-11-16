package genui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	Name     string `json:"name"`
	Schema   string `json:"schema"`
	File     string `json:"file"`
	Valid    bool   `json:"valid"`
	Terminal *bool  `json:"terminal"`
	Known    *bool  `json:"known"`
}

func TestSharedConformanceFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/fixtures/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []fixture `json:"fixtures"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, f := range manifest.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata/fixtures", f.File))
			if err != nil {
				t.Fatal(err)
			}
			if f.Schema == ReceiverResponseSchema {
				obs, e := ReadResponse(raw)
				err = e
				if err == nil {
					if f.Known != nil && obs.Known != *f.Known {
						t.Errorf("known = %v", obs.Known)
					}
					if f.Terminal != nil && obs.Terminal != *f.Terminal {
						t.Errorf("terminal = %v", obs.Terminal)
					}
				}
			} else {
				err = ValidateJSON(f.Schema, raw)
				if err == nil && f.Terminal != nil {
					out, e := DecodeOutcome(raw)
					if e != nil {
						t.Fatal(e)
					}
					if out.Status.Terminal() != *f.Terminal {
						t.Errorf("terminal = %v", out.Status.Terminal())
					}
				}
			}
			if (err == nil) != f.Valid {
				t.Fatalf("valid=%v, error=%v", f.Valid, err)
			}
		})
	}
}
func TestStrictResourceAndNumericBoundaries(t *testing.T) {
	for name, raw := range map[string][]byte{
		"deep":                       []byte(strings.Repeat("[", MaxDepth+2) + "0" + strings.Repeat("]", MaxDepth+2)),
		"large":                      bytes.Repeat([]byte(" "), MaxActionBytes+1),
		"long string":                []byte(`{"status":"ok","result":"` + strings.Repeat("a", MaxStringBytes+1) + `"}`),
		"many items":                 []byte(`{"status":"ok","result":[` + strings.Repeat("0,", MaxCollection) + `0]}`),
		"invalid UTF8":               append([]byte(`{"status":"ok","result":"`), 0xff, '"', '}'),
		"fraction rounds to integer": []byte(`{"status":"ok","result":1.0000000000000000001}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateJSON(OutcomeSchema, raw); err == nil {
				t.Fatal("invalid value accepted")
			}
		})
	}
	for _, raw := range []string{`{"n":9007199254740991}`, `{"n":-9007199254740991}`, `{"n":0}`, `{"n":"9007199254740993"}`, `{"n":"1.0000000000000000001"}`} {
		if _, err := CanonicalJSON([]byte(raw)); err != nil {
			t.Fatalf("supported value %s: %v", raw, err)
		}
	}
}
func TestCanonicalEncodingAndUncertainResponse(t *testing.T) {
	a, err := CanonicalJSON([]byte(`{"z":"<","a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != `{"a":1,"z":"<"}` {
		t.Fatalf("non-JCS encoding %s", a)
	}
	raw := []byte(`{"version":"1.0","agentFunctionResponse":{"functionCallId":"rpc_9","value":{"status":"future_unknown","detail":{"pending":true}}}}`)
	obs, err := ReadResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Known || obs.Terminal || obs.Response.AgentFunctionResponse.Value != nil || len(obs.Unknown) == 0 {
		t.Fatalf("unknown became usable outcome: %+v", obs)
	}
	if err := ValidateJSON(ResponseSchema, raw); err == nil {
		t.Fatal("producer accepts unknown status")
	}
	if _, err = ReadResponse([]byte(`{"version":"1.0","agentFunctionResponse":{"functionCallId":"rpc_9","value":{"status":"partial"}}}`)); err == nil {
		t.Fatal("partial accepted")
	}
}
func TestFormSubmissionAndPublishedSnapshot(t *testing.T) {
	raw, err := os.ReadFile("testdata/fixtures/form.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	var props FormProps
	if err = json.Unmarshal(e.Data.CreateSurface.Components[0].Props, &props); err != nil {
		t.Fatal(err)
	}
	if err = ValidateFormDraft(props, map[string]any{}, false); err != nil {
		t.Fatal(err)
	}
	if err = ValidateFormDraft(props, map[string]any{}, true); err == nil {
		t.Fatal("missing required submit field accepted")
	}
	if err = ValidateFormDraft(props, map[string]any{"name": "Edited", "enabled": true}, true); err != nil {
		t.Fatal(err)
	}
	choices, err := Choices(e)
	if err != nil {
		t.Fatal(err)
	}
	choices[0].CallFunction.Args.Input["new"] = "mutation"
	again, err := Choices(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again[0].CallFunction.Args.Input["new"]; ok {
		t.Fatal("choice mutation leaked into surface")
	}
	props.Submit.CallFunction.Args.Action.Confirmation = "forged"
	if _, err = NewSurface("surface_new", PatternForm, props, DataModel{}); err == nil {
		t.Fatal("published confirmation accepted")
	}
}
func TestFieldAndListSemantics(t *testing.T) {
	for _, file := range []string{"form.json", "list_detail.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata/fixtures", file))
		if err != nil {
			t.Fatal(err)
		}
		e, err := DecodeEnvelope(raw)
		if err != nil {
			t.Fatal(err)
		}
		if file == "form.json" {
			var p FormProps
			if err = json.Unmarshal(e.Data.CreateSurface.Components[0].Props, &p); err != nil {
				t.Fatal(err)
			}
			p.Fields = append(p.Fields, p.Fields[0])
			if _, err = NewSurface("surface_new", PatternForm, p, DataModel{}); err == nil {
				t.Fatal("duplicate field accepted")
			}
		} else {
			var p ListDetailProps
			if err = json.Unmarshal(e.Data.CreateSurface.Components[0].Props, &p); err != nil {
				t.Fatal(err)
			}
			p.Items = append(p.Items, p.Items[0])
			if _, err = NewSurface("surface_new", PatternListDetail, p, DataModel{}); err == nil {
				t.Fatal("duplicate item accepted")
			}
		}
	}
}
