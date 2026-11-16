package genui

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
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

// marshalTyped checks the original values before encoding/json can replace
// malformed strings or map keys. Custom serializers are not contract inputs;
// callers can instead provide their serialized bytes to the strict raw decoders.
func marshalTyped(value any) ([]byte, error) {
	if err := validateTyped(reflect.ValueOf(value), 0); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateTyped(v reflect.Value, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("typed JSON nesting exceeds limit")
	}
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return validateTyped(v.Elem(), depth)
	}
	if v.CanInterface() {
		if raw, ok := v.Interface().(json.RawMessage); ok {
			if raw == nil {
				return nil
			}
			_, err := parseJSON(raw, MaxSurfaceBytes)
			return err
		}
		if _, ok := v.Interface().(json.Marshaler); ok {
			return fmt.Errorf("custom JSON marshaler is not a typed contract value")
		}
		if _, ok := v.Interface().(encoding.TextMarshaler); ok {
			return fmt.Errorf("custom text marshaler is not a typed contract value")
		}
	}
	// Addressable values may implement a serializer only on their pointer.
	if v.CanAddr() && v.Addr().CanInterface() {
		if _, ok := v.Addr().Interface().(json.Marshaler); ok {
			return fmt.Errorf("custom JSON marshaler is not a typed contract value")
		}
		if _, ok := v.Addr().Interface().(encoding.TextMarshaler); ok {
			return fmt.Errorf("custom text marshaler is not a typed contract value")
		}
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("invalid UTF-8 in typed string")
		}
	case reflect.Pointer:
		if !v.IsNil() {
			return validateTyped(v.Elem(), depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			if field.PkgPath != "" {
				// encoding/json promotes exported descendants of an unexported
				// anonymous struct (including a pointer to one). Other private
				// fields are not serialized and must not affect validation.
				t := field.Type
				if t.Kind() == reflect.Pointer {
					t = t.Elem()
				}
				if !field.Anonymous || t.Kind() != reflect.Struct {
					continue
				}
			}
			if !utf8.ValidString(field.Tag.Get("json")) {
				return fmt.Errorf("invalid UTF-8 in JSON field tag")
			}
			if err := validateTyped(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.Len() > MaxCollection {
			return fmt.Errorf("too many typed object members")
		}
		for it := v.MapRange(); it.Next(); {
			if it.Key().Kind() != reflect.String {
				return fmt.Errorf("typed object keys must be strings")
			}
			if err := validateTyped(it.Key(), depth+1); err != nil {
				return err
			}
			if err := validateTyped(it.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		// Plain byte slices encode as base64 strings; RawMessage was checked above.
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		if v.Len() > MaxCollection {
			return fmt.Errorf("too many typed array items")
		}
		for i := 0; i < v.Len(); i++ {
			if err := validateTyped(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
