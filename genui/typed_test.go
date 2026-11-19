package genui

import (
	"encoding/json"
	"os"
	"testing"
)

func typedFixture[T any](t *testing.T, name string) T {
	t.Helper()
	raw, err := os.ReadFile("testdata/fixtures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	var p T
	if err := json.Unmarshal(e.Data.CreateSurface.Components[0].Props, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTypedInputsRejectMalformedUTF8BeforeEncoding(t *testing.T) {
	bad := string([]byte{0xff})
	for name, mutate := range map[string]func(*ApprovalProps){
		"action binding": func(p *ApprovalProps) { p.Choices[0].CallFunction.Args.Action.ID = "action_" + bad },
		"nested input value": func(p *ApprovalProps) {
			p.Choices[0].CallFunction.Args.Input["nested"] = []any{map[string]any{"value": bad}}
		},
		"nested input key": func(p *ApprovalProps) { p.Choices[0].CallFunction.Args.Input[bad] = true },
		"display text":     func(p *ApprovalProps) { p.Title = bad },
	} {
		t.Run(name, func(t *testing.T) {
			p := typedFixture[ApprovalProps](t, "approval")
			mutate(&p)
			if _, err := NewSurface("surface_valid", PatternApproval, p, DataModel{}); err == nil {
				t.Fatal("malformed original string accepted")
			}
		})
	}
	p := typedFixture[ApprovalProps](t, "approval")
	if _, err := NewSurface("surface_"+bad, PatternApproval, p, DataModel{}); err == nil {
		t.Fatal("malformed surface ID accepted")
	}
	env, err := NewSurface("surface_valid", PatternApproval, p, DataModel{})
	if err != nil {
		t.Fatal(err)
	}
	env.ID = "surface_" + bad
	env.Data.CreateSurface.SurfaceID = env.ID
	if _, err := Choices(env); err == nil {
		t.Fatal("malformed typed envelope accepted")
	}
	if err := ValidateOutcome(Outcome{Status: StatusFailed, Code: "failure", Message: bad}); err == nil {
		t.Fatal("malformed typed outcome accepted")
	}
	for name, draft := range map[string]map[string]any{
		"value": {"name": bad}, "key": {bad: "value"},
	} {
		t.Run("draft "+name, func(t *testing.T) {
			p := typedFixture[FormProps](t, "form")
			if err := ValidateFormDraft(p, draft, true); err == nil {
				t.Fatal("malformed typed draft accepted")
			}
			if _, err := NewSurface("surface_valid", PatternForm, p, DataModel{Draft: draft}); err == nil {
				t.Fatal("malformed initial draft accepted")
			}
		})
	}
	form := typedFixture[FormProps](t, "form")
	form.Title = bad
	if err := ValidateFormDraft(form, map[string]any{"name": "valid"}, true); err == nil {
		t.Fatal("malformed form definition accepted")
	}
}

func TestTypedInputsPreserveUnicodeAndReplacementCharacter(t *testing.T) {
	text := "valid_\ufffd_é_😀"
	p := typedFixture[ApprovalProps](t, "approval")
	p.Choices[0].CallFunction.Args.Action.ID = text
	p.Choices[0].CallFunction.Args.Input = map[string]any{text: []any{map[string]any{"text": text}}}
	env, err := NewSurface(text, PatternApproval, p, DataModel{})
	if err != nil {
		t.Fatal(err)
	}
	choices, err := Choices(env)
	if err != nil {
		t.Fatal(err)
	}
	if env.ID != text || choices[0].CallFunction.Args.Action.ID != text {
		t.Fatal("valid Unicode changed")
	}
	if _, ok := choices[0].CallFunction.Args.Input[text]; !ok {
		t.Fatal("valid Unicode map key changed")
	}
	form := typedFixture[FormProps](t, "form")
	if err := ValidateFormDraft(form, map[string]any{"name": text}, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutcome(Outcome{Status: StatusFailed, Code: "failure", Message: text}); err != nil {
		t.Fatal(err)
	}
}

type replacingSerializer string

func (replacingSerializer) MarshalJSON() ([]byte, error) { return []byte(`"replacement"`), nil }

func TestTypedInputsDoNotBypassPreflight(t *testing.T) {
	p := typedFixture[ApprovalProps](t, "approval")
	for name, value := range map[string]any{
		"custom serializer":      replacingSerializer(string([]byte{0xff})),
		"raw invalid UTF8":       json.RawMessage{'"', 0xff, '"'},
		"raw unpaired surrogate": json.RawMessage(`"\ud800"`),
	} {
		t.Run(name, func(t *testing.T) {
			p.Choices[0].CallFunction.Args.Input = map[string]any{"value": value}
			if _, err := NewSurface("surface_valid", PatternApproval, p, DataModel{}); err == nil {
				t.Fatal("lossy serializer accepted")
			}
		})
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	p.Choices[0].CallFunction.Args.Input = cyclic
	if _, err := NewSurface("surface_valid", PatternApproval, p, DataModel{}); err == nil {
		t.Fatal("cyclic input accepted")
	}
}

type privateTypedEmbedded struct {
	Value   string `json:"value"`
	ignored string
}
type privateTypedString string

func TestTypedJSONVisibleAnonymousFields(t *testing.T) {
	bad := string([]byte{0xff})
	for name, value := range map[string]any{
		"promoted value":   struct{ privateTypedEmbedded }{privateTypedEmbedded{Value: bad}},
		"promoted pointer": struct{ *privateTypedEmbedded }{&privateTypedEmbedded{Value: bad}},
		"named anonymous": struct {
			privateTypedEmbedded `json:"named"`
		}{privateTypedEmbedded{Value: bad}},
	} {
		t.Run(name, func(t *testing.T) {
			p := typedFixture[ApprovalProps](t, "approval")
			p.Choices[0].CallFunction.Args.Input = map[string]any{"nested": value}
			if _, err := NewSurface("surface_valid", PatternApproval, p, DataModel{}); err == nil {
				t.Fatal("malformed JSON-visible promoted field accepted")
			}
		})
	}
	good := "valid_\ufffd_é_😀"
	for name, value := range map[string]any{
		"valid promoted with private garbage": struct{ privateTypedEmbedded }{privateTypedEmbedded{Value: good, ignored: bad}},
		"valid promoted pointer":              struct{ *privateTypedEmbedded }{&privateTypedEmbedded{Value: good, ignored: bad}},
		"nil embedded pointer":                struct{ *privateTypedEmbedded }{},
		"private named struct": struct {
			hidden privateTypedEmbedded
			Value  string `json:"value"`
		}{hidden: privateTypedEmbedded{Value: bad}, Value: good},
		"ignored anonymous": struct {
			privateTypedEmbedded `json:"-"`
			Value                string `json:"value"`
		}{privateTypedEmbedded: privateTypedEmbedded{Value: bad}, Value: good},
		"private scalar anonymous": struct {
			privateTypedString
			Value string `json:"value"`
		}{privateTypedString: privateTypedString(bad), Value: good},
	} {
		t.Run(name, func(t *testing.T) {
			p := typedFixture[ApprovalProps](t, "approval")
			p.Choices[0].CallFunction.Args.Input = map[string]any{"nested": value}
			e, err := NewSurface("surface_valid", PatternApproval, p, DataModel{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Choices(e); err != nil {
				t.Fatal(err)
			}
		})
	}
}
