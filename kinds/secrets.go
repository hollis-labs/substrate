package kinds

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/security/redact"
)

// ErrSecretValue contains no rejected content. Callers must not log input when
// admission fails; values are refused, never silently scrubbed or persisted.
var ErrSecretValue = errors.New("secret-like value refused; use a reference")

// ValidateNoSecretsJSON checks decoded strings and nested object keys with the
// shared credential detector. It performs no resolution, network or storage IO.
// The five Registry content kinds run this before structural validation, so a
// schema error cannot echo a detected credential. Services may also apply it to
// envelopes and older kinds. This is conservative detection, not proof that an
// arbitrary opaque string contains no secret; upstream admission still matters.
func ValidateNoSecretsJSON(data []byte) error {
	if err := ValidateJSONValue(data); err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	return refuseSecrets(value)
}

func refuseSecrets(value any) error {
	switch v := value.(type) {
	case string:
		if len(redact.Default().Scan(v)) > 0 {
			return ErrSecretValue
		}
	case []any:
		for _, item := range v {
			if err := refuseSecrets(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			// Sensitive settings must use a typed reference, rather than a literal,
			// placeholder or nested value that could be mistaken for a credential.
			if redact.SensitiveName(key) && !secretReference(item) {
				return ErrSecretValue
			}
			if len(redact.Default().Scan(key)) > 0 {
				return ErrSecretValue
			}
			if err := refuseSecrets(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func secretReference(value any) bool {
	ref, ok := value.(map[string]any)
	if !ok || (len(ref) != 3 && len(ref) != 4) {
		return false
	}
	if len(ref) == 4 {
		uri, ok := ref["uri"].(string)
		if !ok || uri == "" {
			return false
		}
	}
	authority, a := ref["authority"].(string)
	kind, k := ref["kind"].(string)
	id, i := ref["id"].(string)
	return a && k && i && authority != "" && kind == "secret" && id != ""
}
