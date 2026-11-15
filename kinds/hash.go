package kinds

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// HashAlgorithm identifies the encoding and exact projection, not just SHA-256.
const HashAlgorithm = "tr1-jcs-sha256/1"

// DefinitionHash validates an authored tool and hashes exactly the TR-1
// projection. Identity and display-only changes remain separate revision data.
func DefinitionHash(t Tool) (string, error) {
	projection, err := DefinitionProjection(t)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(projection)
	return hex.EncodeToString(sum[:]), nil
}

// DefinitionProjection returns the RFC 8785 bytes used by DefinitionHash.
// Optional absent fields stay absent; explicit null remains null. Array order
// is significant. CLI hashes its grammar instead of MCP schema fields.
func DefinitionProjection(t Tool) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	var value any
	if t.Source == "cli" {
		value = t.Definition.CLI
	} else {
		value = struct {
			Name         string          `json:"name"`
			Description  json.RawMessage `json:"description,omitempty"`
			InputSchema  json.RawMessage `json:"input_schema,omitempty"`
			OutputSchema json.RawMessage `json:"output_schema,omitempty"`
			Annotations  json.RawMessage `json:"annotations,omitempty"`
		}{t.Definition.Name, t.Definition.Description, t.Definition.InputSchema, t.Definition.OutputSchema, t.Definition.Annotations}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicalJSON(raw)
}

// canonicalJSON delegates serialization to RFC 8785's existing implementation.
// The extra numeric admission rule prevents silently rounding large integer
// identifiers/constraints or underflowing a nonzero value to zero.
func canonicalJSON(data []byte) ([]byte, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if n, ok := token.(json.Number); ok {
			f, err := strconv.ParseFloat(string(n), 64)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, fmt.Errorf("number outside finite binary64 domain")
			}
			if math.Trunc(f) == f && math.Abs(f) > 9007199254740991 {
				return nil, fmt.Errorf("integer outside safe binary64 range; encode exact large values as strings")
			}
			significand := strings.Split(strings.ToLower(string(n)), "e")[0]
			if f == 0 && strings.ContainsAny(significand, "123456789") {
				return nil, fmt.Errorf("number underflows binary64; encode exact small values as strings")
			}
		}
	}
	out, err := jsoncanonicalizer.Transform(data)
	if err != nil {
		return nil, fmt.Errorf("invalid canonical JSON: %w", err)
	}
	return out, nil
}
