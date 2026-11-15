package kinds_test

import (
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestJSONValueAdmissionBeforeNativeDecoding(t *testing.T) {
	for _, value := range []string{`null`, `{"number":9007199254740991}`, `{"number":1e0}`, `{"number":0.1}`, `{"number":5e-324}`, `{"exact":"9007199254740993"}`} {
		if err := kinds.ValidateJSONValue([]byte(value)); err != nil {
			t.Errorf("valid %s: %v", value, err)
		}
	}
	for _, value := range []string{`{"number":9007199254740993}`, `{"nested":[1e-400]}`, `{"number":1e400}`, `{"key":1,"key":2}`, `{"text":"\ud800"}`, `{`} {
		if err := kinds.ValidateJSONValue([]byte(value)); err == nil {
			t.Errorf("admitted %s", value)
		}
	}
}
