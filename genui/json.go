package genui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

var integerLexeme = regexp.MustCompile(`^(0|-[1-9][0-9]*|[1-9][0-9]*)$`)

// CanonicalJSON validates the profile before any float conversion. Numbers must
// use integer lexical spelling and lie within the exact JavaScript integer range.
// It rejects duplicate keys, invalid Unicode and bounded-resource violations.
// RFC 8785 serialization and unchanged integers give one semantic encoding for
// this profile; transport invocation IDs and proofs are not excluded here.
func CanonicalJSON(raw []byte) ([]byte, error) {
	_, err := parseJSON(raw, MaxSurfaceBytes)
	if err != nil {
		return nil, err
	}
	wrapped, err := jsoncanonicalizer.Transform(append(append([]byte{'['}, raw...), ']'))
	if err != nil {
		return nil, err
	}
	return wrapped[1 : len(wrapped)-1], nil
}
func parseJSON(raw []byte, limit int) (any, error) {
	if len(raw) > limit {
		return nil, fmt.Errorf("JSON exceeds %d bytes", limit)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := readValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing or malformed JSON")
	}
	// Existing shared canonical validation rejects unpaired escaped surrogates;
	// encoding/json alone substitutes U+FFFD and would lose the malformed input.
	if _, err := jsoncanonicalizer.Transform(append(append([]byte{'['}, raw...), ']')); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return v, nil
}
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d", MaxDepth)
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("invalid object key")
				}
				if len(k) > 256 {
					return nil, fmt.Errorf("object key too long")
				}
				if _, ok := m[k]; ok {
					return nil, fmt.Errorf("duplicate object key")
				}
				if len(m) >= MaxCollection {
					return nil, fmt.Errorf("too many object members")
				}
				v, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid object")
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				if len(a) >= MaxCollection {
					return nil, fmt.Errorf("too many array items")
				}
				v, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid array")
			}
			return a, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter")
		}
	case json.Number:
		if !integerLexeme.MatchString(string(x)) {
			return nil, fmt.Errorf("only canonical integer numeric lexemes are supported")
		}
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil || n < -MaxSafeInteger || n > MaxSafeInteger {
			return nil, fmt.Errorf("integer outside safe range")
		}
		return n, nil
	case string:
		if len(x) > MaxStringBytes {
			return nil, fmt.Errorf("string too long")
		}
		return x, nil
	case nil, bool:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported JSON token")
	}
}
