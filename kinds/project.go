package kinds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

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

// ParseProjectJSON validates before decoding. No server verification stamps are
// accepted in the authored project descriptor.
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
func ParseProjectYAML(data []byte) (*Project, error) {
	d := yaml.NewDecoder(bytes.NewReader(data))
	var value map[string]any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("project: expected one YAML document")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return ParseProjectJSON(raw)
}

func (p Project) Validate() error { return validateValue(ProjectSchema, p) }

// CanonicalID leaves the descriptor's immutable slug unchanged.
func (p Project) CanonicalID() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return "project:" + p.ID, nil
}
