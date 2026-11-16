package genui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// NewSurface materializes one complete reviewed root and validates a detached
// snapshot. It never registers an operation or grants authority to its choices.
func NewSurface(id string, pattern Pattern, props any, data DataModel) (Envelope, error) {
	component, ok := patternComponents[pattern]
	if !ok {
		return Envelope{}, fmt.Errorf("unknown reviewed pattern")
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return Envelope{}, err
	}
	e := Envelope{V: 1, ID: id, Type: EnvelopeType, TypeVersion: TypeVersion, Presentation: "inline", Data: SurfaceMessage{Version: ProtocolVersion, CreateSurface: Surface{SurfaceID: id, CatalogID: CatalogID, Components: []Component{{ID: "root", Component: component, Props: raw}}, DataModel: data, Metadata: Metadata{Extensions: Extensions{Surface: SurfaceVersion{ContractVersion: ContractVersion}, Pattern: PatternMetadata{ID: pattern, Version: TypeVersion}}}}}}
	raw, err = json.Marshal(e)
	if err != nil {
		return Envelope{}, err
	}
	return DecodeEnvelope(raw)
}

var patternComponents = map[Pattern]string{PatternApproval: "GenuiApproval", PatternForm: "GenuiForm", PatternListDetail: "GenuiListDetail"}

func validateSurface(e Envelope) error {
	s := e.Data.CreateSurface
	if e.ID != s.SurfaceID {
		return fmt.Errorf("envelope and surface IDs differ")
	}
	if len(s.Components) != 1 {
		return fmt.Errorf("one complete reviewed root is required")
	}
	root := s.Components[0]
	pattern := s.Metadata.Extensions.Pattern.ID
	if patternComponents[pattern] != root.Component {
		return fmt.Errorf("pattern and component differ")
	}
	if pattern != PatternForm && s.DataModel.Draft != nil {
		return fmt.Errorf("draft is only available on form patterns")
	}
	switch pattern {
	case PatternApproval:
		var p ApprovalProps
		if err := json.Unmarshal(root.Props, &p); err != nil {
			return err
		}
		for _, c := range p.Choices {
			if err := validateChoice(c); err != nil {
				return err
			}
		}
	case PatternForm:
		var p FormProps
		if err := json.Unmarshal(root.Props, &p); err != nil {
			return err
		}
		if err := validateChoice(p.Submit); err != nil {
			return err
		}
		if _, ok := p.Submit.CallFunction.Args.Input["draft"]; ok {
			return fmt.Errorf("form submit base input reserves draft for the complete submitted draft")
		}
		return ValidateFormDraft(p, s.DataModel.Draft, false)
	case PatternListDetail:
		var p ListDetailProps
		if err := json.Unmarshal(root.Props, &p); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, item := range p.Items {
			if seen[item.ID] {
				return fmt.Errorf("duplicate list item ID")
			}
			seen[item.ID] = true
		}
		if p.SelectedID != "" && !seen[p.SelectedID] {
			return fmt.Errorf("selected item is not in list")
		}
		if p.Detail != nil && p.SelectedID == "" {
			return fmt.Errorf("detail requires a selected item")
		}
	}
	return nil
}
func validateChoice(c Choice) error {
	if c.CallFunction.Args.Action.Confirmation != "" {
		return fmt.Errorf("published choices cannot contain a confirmation proof")
	}
	return nil
}

// ValidateFormDraft checks field semantics and values. Initial drafts may omit
// required values; complete submissions may not. It does not authorize a change.
func ValidateFormDraft(p FormProps, draft map[string]any, submitted bool) error {
	rawProps, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = ValidateJSON("form-props/1", rawProps); err != nil {
		return err
	}
	fields := map[string]FormField{}
	for _, f := range p.Fields {
		if _, ok := fields[f.Key]; ok {
			return fmt.Errorf("duplicate form field key")
		}
		fields[f.Key] = f
		if f.Type != "integer" && (f.Min != nil || f.Max != nil) {
			return fmt.Errorf("numeric constraints on noninteger field")
		}
		if f.Type != "text" && f.Type != "textarea" && f.MaxLength != nil {
			return fmt.Errorf("length constraint on nontext field")
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("field minimum exceeds maximum")
		}
	}
	// Round-trip only after the raw decoder has proved the numeric profile. For
	// typed callers this verifies the JSON values they actually supplied.
	raw, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	if draft == nil {
		raw = []byte("{}")
	}
	v, err := parseJSON(raw, MaxActionBytes)
	if err != nil {
		return err
	}
	values, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("draft must be an object")
	}
	for key, value := range values {
		f, ok := fields[key]
		if !ok {
			return fmt.Errorf("draft field is not declared")
		}
		switch f.Type {
		case "text", "textarea":
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("text field requires string")
			}
			if f.MaxLength != nil && utf8.RuneCountInString(s) > *f.MaxLength {
				return fmt.Errorf("field length exceeds maximum")
			}
			if submitted && f.Required && s == "" {
				return fmt.Errorf("required field is empty")
			}
		case "checkbox":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("checkbox requires boolean")
			}
		case "integer":
			n, ok := value.(int64)
			if !ok {
				return fmt.Errorf("integer field requires integer")
			}
			if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
				return fmt.Errorf("integer field outside range")
			}
		default:
			return fmt.Errorf("unknown form field type")
		}
	}
	if submitted {
		for k, f := range fields {
			if f.Required {
				if _, ok := values[k]; !ok {
					return fmt.Errorf("required field is missing")
				}
			}
		}
	}
	return nil
}

// Choices returns detached offered choices. It is presentation data; a host
// must still resolve them to its own registered operation and current policy.
func Choices(e Envelope) ([]Choice, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	e, err = DecodeEnvelope(raw)
	if err != nil {
		return nil, err
	}
	root := e.Data.CreateSurface.Components[0]
	switch root.Component {
	case "GenuiApproval":
		var p ApprovalProps
		if err = json.Unmarshal(root.Props, &p); err != nil {
			return nil, err
		}
		return p.Choices, nil
	case "GenuiForm":
		var p FormProps
		if err = json.Unmarshal(root.Props, &p); err != nil {
			return nil, err
		}
		return []Choice{p.Submit}, nil
	default:
		return nil, nil
	}
}

// IntegerValue reads a safe integer retained by a strict decoder. It is useful
// when handling JSON-valued form inputs without converting through float64.
func IntegerValue(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, n >= -MaxSafeInteger && n <= MaxSafeInteger
	case json.Number:
		x, err := strconv.ParseInt(string(n), 10, 64)
		return x, err == nil && integerLexeme.MatchString(string(n)) && x >= -MaxSafeInteger && x <= MaxSafeInteger
	}
	return 0, false
}
