package gateway

import (
	"encoding/json"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes"
	"github.com/hollis-labs/substrate/genui"
)

// RegisterEnvelope adds the profile to a host-owned existing registry. It does
// not modify the core catalog or silently replace another registration. Raw
// input must still pass genui.DecodeEnvelope for bounded semantic validation.
func RegisterEnvelope(registry *envelopes.Registry) error {
	if registry == nil {
		return ErrUnavailable
	}
	raw, err := genui.ContractSchema()
	if err != nil {
		return err
	}
	var schema map[string]any
	if err = json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	schema["$ref"] = "#/$defs/SurfaceMessage"
	raw, err = json.Marshal(schema)
	if err != nil {
		return err
	}
	doc, err := envelopes.NewSchemaDocument("https://github.com/hollis-labs/substrate/genui/contract/1", raw)
	if err != nil {
		return err
	}
	return registry.RegisterType(envelopes.TypeSpec{Name: genui.EnvelopeType, Version: genui.TypeVersion, PluginID: "genui", DataSchemaDocument: doc, Description: "Complete reviewed GenUI surface; action authority remains with the host."})
}
