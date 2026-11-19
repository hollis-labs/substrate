package kinds

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// Service describes an authored service contract. Interfaces describe intended
// access, not a running listener, deployment or operational readiness.
type Service struct {
	ContentIdentity
	Host               string             `json:"host"`
	CerberusResourceID string             `json:"cerberus_resource_id"`
	Port               *int               `json:"port,omitempty"`
	HealthURL          string             `json:"health_url,omitempty"`
	DeployMethod       string             `json:"deploy_method"`
	Operator           string             `json:"operator"`
	Description        string             `json:"description"`
	Project            Reference          `json:"project"`
	Interfaces         []ServiceInterface `json:"interfaces"`
	Config             []Reference        `json:"config,omitempty"`
	References         []Reference        `json:"references,omitempty"`
}

type ServiceInterface struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// Config is shared authored configuration. App-owned configuration remains in
// the app database. SecretRefs are locators, never resolved credential values.
type Config struct {
	ContentIdentity
	Target         Reference            `json:"target"`
	SettingsSchema json.RawMessage      `json:"settings_schema"`
	Scope          string               `json:"scope"`
	Settings       map[string]any       `json:"settings"`
	SecretRefs     map[string]Reference `json:"secret_refs,omitempty"`
	References     []Reference          `json:"references,omitempty"`
}

// Knowledge retains a standalone authored body and source provenance. Upstream
// identifies a Tesseract revision; it neither transfers authority nor writes
// back to that source.
type Knowledge struct {
	ContentIdentity
	Format     string              `json:"format"`
	Body       string              `json:"body"`
	Provenance []Reference         `json:"provenance"`
	Upstream   *VersionedReference `json:"upstream,omitempty"`
	References []Reference         `json:"references,omitempty"`
}

// MCPServer describes a Tether-owned server definition by immutable source
// reference. It contains no process credentials, spawn configuration or grant.
type MCPServer struct {
	ContentIdentity
	Transport  string             `json:"transport"`
	Source     VersionedReference `json:"source"`
	Tools      []Reference        `json:"tools,omitempty"`
	Config     []Reference        `json:"config,omitempty"`
	References []Reference        `json:"references,omitempty"`
}

// LaunchProfile references Tether-owned launch semantics. Permissions, cascade
// resolution, provider overrides and launch execution remain with Tether.
type LaunchProfile struct {
	ContentIdentity
	Source     VersionedReference `json:"source"`
	References []Reference        `json:"references,omitempty"`
}

func (v Service) Validate() error       { return validateValue(ServiceSchema, v) }
func (v Config) Validate() error        { return validateValue(ConfigSchema, v) }
func (v Knowledge) Validate() error     { return validateValue(KnowledgeSchema, v) }
func (v MCPServer) Validate() error     { return validateValue(MCPServerSchema, v) }
func (v LaunchProfile) Validate() error { return validateValue(LaunchProfileSchema, v) }

func isRegistryKind(schema string) bool {
	switch schema {
	case ServiceSchema, ConfigSchema, KnowledgeSchema, MCPServerSchema, LaunchProfileSchema:
		return true
	}
	return false
}

// validateConfig compiles only the supplied schema bundle. The resolver's
// default loader refuses external references; admission performs no IO.
func validateConfig(data []byte) error {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Target.Kind != c.Scope {
		return fmt.Errorf("config/1: target kind must match scope")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(c.SettingsSchema, &schema); err != nil {
		return fmt.Errorf("config/1: invalid settings_schema")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("config/1: settings_schema cannot be resolved locally")
	}
	if err := resolved.Validate(c.Settings); err != nil {
		return fmt.Errorf("config/1: settings do not match settings_schema")
	}
	return nil
}
