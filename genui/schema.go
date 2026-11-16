package genui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	EnvelopeSchema         = "envelope/1"
	CallSchema             = "call/1"
	OutcomeSchema          = "outcome/1"
	ResponseSchema         = "response/1"
	ReceiverResponseSchema = "response-read/1"
)

//go:embed schema/*.json
var contractFiles embed.FS
var definitions = map[string]string{EnvelopeSchema: "Envelope", CallSchema: "Call", OutcomeSchema: "Outcome", ResponseSchema: "Response", ReceiverResponseSchema: "ReceiverResponse", "form-props/1": "FormProps"}

type compiledSchema struct {
	once     sync.Once
	resolved *jsonschema.Resolved
	err      error
}

var compiled = map[string]*compiledSchema{EnvelopeSchema: {}, CallSchema: {}, OutcomeSchema: {}, ResponseSchema: {}, ReceiverResponseSchema: {}, "form-props/1": {}}

// ContractSchema returns an independent copy of the canonical definitions.
func ContractSchema() ([]byte, error) { return contractFiles.ReadFile("schema/contract.schema.json") }

// Catalog returns the native A2UI reviewed-pattern catalog.
func Catalog() ([]byte, error) { return contractFiles.ReadFile("schema/catalog.json") }

// Schema returns a self-contained schema for one public entry point.
func Schema(name string) ([]byte, error) {
	def, ok := definitions[name]
	if !ok {
		return nil, fmt.Errorf("unknown GenUI schema %q", name)
	}
	raw, err := ContractSchema()
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	doc["$ref"] = "#/$defs/" + def
	return json.Marshal(doc)
}
func ValidateJSON(name string, raw []byte) error {
	limit := MaxActionBytes
	if name == EnvelopeSchema {
		limit = MaxSurfaceBytes
	}
	v, err := parseJSON(raw, limit)
	if err != nil {
		return err
	}
	if err = validateSchema(name, v); err != nil {
		return err
	}
	switch name {
	case EnvelopeSchema:
		var e Envelope
		if err = decodeValue(v, &e); err != nil {
			return err
		}
		return validateSurface(e)
	case OutcomeSchema:
		var o Outcome
		if err = decodeValue(v, &o); err != nil {
			return err
		}
		return validateOutcomeDepth(&o, 0)
	case ResponseSchema, ReceiverResponseSchema:
		if name == ReceiverResponseSchema {
			_, err := ReadResponse(raw)
			return err
		}
		var r Response
		if err = decodeValue(v, &r); err != nil {
			return err
		}
		if r.AgentFunctionResponse.Value != nil {
			return validateOutcomeDepth(r.AgentFunctionResponse.Value, 0)
		}
	}
	return nil
}
func validateSchema(name string, v any) error {
	c, ok := compiled[name]
	if !ok {
		return fmt.Errorf("unknown GenUI schema %q", name)
	}
	c.once.Do(func() {
		raw, err := Schema(name)
		if err != nil {
			c.err = err
			return
		}
		var s jsonschema.Schema
		if err = json.Unmarshal(raw, &s); err != nil {
			c.err = err
			return
		}
		c.resolved, c.err = s.Resolve(nil)
	})
	if c.err != nil {
		return c.err
	}
	// Integers already proved safe before this schema evaluator's binary64 view.
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var view any
	if err = json.Unmarshal(b, &view); err != nil {
		return err
	}
	if err = c.resolved.Validate(view); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
func decodeValue(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func decode(name string, raw []byte, out any) error {
	if err := ValidateJSON(name, raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func DecodeEnvelope(raw []byte) (Envelope, error) {
	var e Envelope
	err := decode(EnvelopeSchema, raw, &e)
	return e, err
}
func DecodeCall(raw []byte) (Call, error) {
	var c Call
	err := decode(CallSchema, raw, &c)
	return c, err
}
func DecodeOutcome(raw []byte) (Outcome, error) {
	var o Outcome
	err := decode(OutcomeSchema, raw, &o)
	return o, err
}
func ValidateOutcome(o Outcome) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return ValidateJSON(OutcomeSchema, raw)
}
func validateOutcomeDepth(o *Outcome, depth int) error {
	if depth > 2 {
		return fmt.Errorf("recorded conflict nesting exceeds two")
	}
	if o.Recorded != nil {
		return validateOutcomeDepth(o.Recorded, depth+1)
	}
	return nil
}

// ReadResponse validates native protocol shape, preserving a future outcome as
// nonterminal evidence. Known-but-malformed and retired partial outcomes refuse.
func ReadResponse(raw []byte) (ResponseObservation, error) {
	var result ResponseObservation
	v, err := parseJSON(raw, MaxActionBytes)
	if err != nil {
		return result, err
	}
	if err = validateSchema(ReceiverResponseSchema, v); err != nil {
		return result, err
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 2 || m["version"] != ProtocolVersion {
		return result, fmt.Errorf("invalid response envelope")
	}
	r, ok := m["agentFunctionResponse"].(map[string]any)
	if !ok || len(r) != 2 {
		return result, fmt.Errorf("invalid function response")
	}
	id, ok := r["functionCallId"].(string)
	if !ok || id == "" || len(id) > 256 {
		return result, fmt.Errorf("invalid function call ID")
	}
	if value, ok := r["value"]; ok {
		out, ok := value.(map[string]any)
		if !ok {
			return result, fmt.Errorf("outcome must be an object")
		}
		status, ok := out["status"].(string)
		if !ok || status == "" || len(status) > 128 || status == "partial" {
			return result, fmt.Errorf("invalid outcome status")
		}
		if !Status(status).Known() {
			b, err := json.Marshal(out)
			if err != nil {
				return result, err
			}
			result.Response = Response{Version: ProtocolVersion, AgentFunctionResponse: FunctionResponse{FunctionCallID: id}}
			result.Unknown = b
			return result, nil
		}
	}
	if err = ValidateJSON(ResponseSchema, raw); err != nil {
		return result, err
	}
	if err = decodeValue(v, &result.Response); err != nil {
		return result, err
	}
	if o := result.Response.AgentFunctionResponse.Value; o != nil {
		result.Known = true
		result.Terminal = o.Status.Terminal()
	}
	return result, nil
}
