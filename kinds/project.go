package kinds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Class preserves RC1's integer classes and literal "lib" in JSON.
type Class string

const (
	ClassApp     Class = "1"
	ClassTool    Class = "2"
	ClassProduct Class = "3"
	ClassLibrary Class = "lib"
)

func (c Class) MarshalJSON() ([]byte, error) {
	switch c {
	case ClassApp, ClassTool, ClassProduct:
		return []byte(c), nil
	case ClassLibrary:
		return []byte(`"lib"`), nil
	default:
		return nil, fmt.Errorf("class: unsupported value %q", c)
	}
}

func (c *Class) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value {
	case float64(1):
		*c = ClassApp
	case float64(2):
		*c = ClassTool
	case float64(3):
		*c = ClassProduct
	case "lib":
		*c = ClassLibrary
	default:
		return fmt.Errorf("class: expected integer 1, 2, 3 or string lib")
	}
	return nil
}

type Project struct {
	Schema      string         `json:"schema"`
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Class       Class          `json:"class"`
	Lifecycle   string         `json:"lifecycle"`
	Visibility  string         `json:"visibility"`
	Owner       string         `json:"owner"`
	Operator    string         `json:"operator,omitempty"`
	Org         string         `json:"org,omitempty"`
	Repo        string         `json:"repo,omitempty"`
	Location    string         `json:"location,omitempty"`
	Interfaces  []Interface    `json:"interfaces"`
	Substrate   *Substrate     `json:"substrate,omitempty"`
	Deploy      *Deploy        `json:"deploy,omitempty"`
	Release     *Release       `json:"release,omitempty"`
	Edges       []ProjectEdge  `json:"edges,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type Interface struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Auth string `json:"auth,omitempty"`
}

type Substrate struct {
	Variant string `json:"variant"`
	Profile string `json:"profile"`
	Version string `json:"version"`
}

type Deploy struct {
	Fleet    string `json:"fleet,omitempty"`
	Cerberus string `json:"cerberus,omitempty"`
}

type Release struct {
	Tagging string `json:"tagging"`
}

type ProjectEdge struct {
	Type EdgeType `json:"type"`
	To   string   `json:"to"`
}

// ParseProjectJSON validates descriptor structure before decoding. It does not
// certify the operational class bar (operator, enforced auth, deployment or
// promotion eligibility). No server verification stamps are accepted here.
func ParseProjectJSON(data []byte) (*Project, error) {
	var p Project
	if err := decode(ProjectSchema, data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ParseProjectYAML accepts one YAML document with string object keys. Duplicate
// keys and trailing documents are errors. YAML is converted to the same JSON
// validation path used by API callers; it has no separate project dialect.
// This is parse-only support: yaml.Marshal(Project) is not a supported encoding.
// Use json.Marshal for a typed roundtrip or retain the authored YAML source.
func ParseProjectYAML(data []byte) (*Project, error) {
	d := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := d.Decode(&doc); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("project: expected one YAML document")
	}
	// yaml.v3 decodes a scalar such as 1e-400 to float64(0). Inspect
	// original scalar lexemes before any native-number conversion.
	if err := validateYAMLNumbers(&doc); err != nil {
		return nil, err
	}
	var value map[string]any
	if err := doc.Decode(&value); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return ParseProjectJSON(raw)
}

var yamlDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func validateYAMLNumbers(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		lexeme := strings.ReplaceAll(n.Value, "_", "")
		numeric := n.Tag == "!!int" || n.Tag == "!!float"
		// The resolver labels an overflowing implicit number as a string.
		// Quoted and explicitly tagged strings are intentional strings.
		implicitNumber := n.Style == 0 && n.Tag == "!!str" && yamlDecimal.MatchString(lexeme)
		if numeric || implicitNumber {
			number := lexeme
			if n.Tag == "!!int" {
				integer, ok := new(big.Int).SetString(lexeme, 0)
				if !ok {
					return fmt.Errorf("project YAML line %d: invalid integer", n.Line)
				}
				number = integer.String()
			} else {
				// Normalize YAML decimal spelling, without converting its value.
				number = strings.TrimPrefix(number, "+")
				parts := strings.SplitN(strings.ToLower(number), "e", 2)
				mantissa := parts[0]
				if strings.HasPrefix(mantissa, ".") {
					mantissa = "0" + mantissa
				}
				if strings.HasPrefix(mantissa, "-.") {
					mantissa = "-0" + mantissa[1:]
				}
				if strings.HasSuffix(mantissa, ".") {
					mantissa += "0"
				}
				sign := ""
				if strings.HasPrefix(mantissa, "-") {
					sign, mantissa = "-", mantissa[1:]
				}
				decimal := strings.SplitN(mantissa, ".", 2)
				whole := strings.TrimLeft(decimal[0], "0")
				if whole == "" {
					whole = "0"
				}
				mantissa = sign + whole
				if len(decimal) == 2 {
					mantissa += "." + decimal[1]
				}
				number = mantissa
				if len(parts) == 2 {
					number += "e" + parts[1]
				}
			}
			if _, err := canonicalJSON([]byte("[" + number + "]")); err != nil {
				return fmt.Errorf("project YAML line %d: %w", n.Line, err)
			}
		}
	}
	// Anchored scalars occur in Content at their definition; do not follow
	// Alias links, which may form cycles. The normal decoder checks aliases.
	for _, child := range n.Content {
		if err := validateYAMLNumbers(child); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks descriptor structure, not operational conformance.
func (p Project) Validate() error { return validateValue(ProjectSchema, p) }

// CanonicalID leaves the descriptor's immutable slug unchanged. A valid ID
// establishes neither class-bar compliance nor permission to deploy or launch.
func (p Project) CanonicalID() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return "project:" + p.ID, nil
}
