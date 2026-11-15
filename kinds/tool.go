package kinds

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Tool is an authored definition, not a review response. Effective
// classification and reviewer identity cannot be supplied through this type.
type Tool struct {
	Schema     string         `json:"schema"`
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Origin     string         `json:"origin,omitempty"`
	Lifecycle  string         `json:"lifecycle"`
	Owner      string         `json:"owner,omitempty"`
	Definition Definition     `json:"definition"`
	Aliases    []Alias        `json:"aliases,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type Alias struct {
	System string `json:"system"`
	Alias  string `json:"alias"`
}

// RawMessage fields preserve absent (nil), explicit null, and the supplied JSON
// value. They must not be collapsed into pointers before computing a hash.
type Definition struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  json.RawMessage `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
	Icons        json.RawMessage `json:"icons,omitempty"`
	Meta         json.RawMessage `json:"meta,omitempty"`
	CLI          *CLIGrammar     `json:"cli,omitempty"`
	SourceRef    string          `json:"source_ref,omitempty"`
	SeenVia      string          `json:"seen_via,omitempty"`
}

type CLIGrammar struct {
	Signature      string   `json:"signature"` // program[/subcommand...] with no flags or paths
	HelpTextDigest string   `json:"help_text_digest"`
	KnownFlags     []string `json:"known_flags"`
}

// Classification is a value contract; it does not grant authority or decide
// whether an approver may set an effective classification.
type Classification struct {
	SideEffect      []string      `json:"side_effect,omitempty"`
	Reversibility   string        `json:"reversibility,omitempty"`
	Idempotent      *bool         `json:"idempotent,omitempty"`
	DataSensitivity string        `json:"data_sensitivity,omitempty"`
	Egress          string        `json:"egress,omitempty"`
	SecretAccess    string        `json:"secret_access,omitempty"`
	Scopes          []string      `json:"scopes,omitempty"`
	ApprovalTier    string        `json:"approval_tier,omitempty"`
	CostTier        string        `json:"cost_tier,omitempty"`
	InputDependent  *bool         `json:"input_dependent,omitempty"`
	RuleRef         string        `json:"rule_ref,omitempty"`
	ArgRules        string        `json:"arg_rules,omitempty"`
	FlagPatterns    []FlagPattern `json:"flag_patterns,omitempty"`
}

type FlagPattern struct {
	Pattern    string   `json:"pattern"`
	SideEffect []string `json:"side_effect"`
	Reason     string   `json:"reason"`
}

func (c Classification) Validate() error { return validateValue(ClassificationSchema, c) }

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ToolID constructs TR-1 identities without case folding, aliases, path cleanup
// or authority-dependent namespaces. CLI origin must be empty.
func ToolID(source, origin, name string) (string, error) {
	if source == "cli" {
		if origin != "" {
			return "", fmt.Errorf("CLI identity has no origin")
		}
		for _, p := range strings.Split(name, "/") {
			if !segment.MatchString(p) || p == "." || p == ".." {
				return "", fmt.Errorf("invalid CLI signature %q", name)
			}
		}
		return "tool:cli/" + name, nil
	}
	switch source {
	case "mcp", "builtin", "skill", "plugin":
	default:
		return "", fmt.Errorf("unsupported tool source %q", source)
	}
	if !segment.MatchString(origin) || !segment.MatchString(name) || origin == "." || origin == ".." || name == "." || name == ".." {
		return "", fmt.Errorf("tool origin and name must be nonempty identity segments")
	}
	return "tool:" + source + "/" + origin + "/" + name, nil
}

func ParseToolJSON(data []byte) (*Tool, error) {
	var t Tool
	if err := decode(ToolSchema, data, &t); err != nil {
		return nil, err
	}
	if err := t.validateIdentity(); err != nil {
		return nil, err
	}
	return &t, nil
}

func (t Tool) validateIdentity() error {
	name := t.Definition.Name
	if t.Source == "cli" {
		name = t.Definition.CLI.Signature
	}
	want, err := ToolID(t.Source, t.Origin, name)
	if err != nil {
		return err
	}
	if t.ID != want {
		return fmt.Errorf("id: expected %q from source and definition, got %q", want, t.ID)
	}
	seen := map[Alias]bool{}
	for _, a := range t.Aliases {
		if seen[a] {
			return fmt.Errorf("aliases: duplicate (%s, %s)", a.System, a.Alias)
		}
		seen[a] = true
	}
	return nil
}

func (t Tool) Validate() error {
	if err := validateValue(ToolSchema, t); err != nil {
		return err
	}
	return t.validateIdentity()
}
