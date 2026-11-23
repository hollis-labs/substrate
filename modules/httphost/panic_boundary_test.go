package httphost

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/panics"
)

func TestInvalidMountReturnsPanicReference(t *testing.T) {
	err := handle(http.NewServeMux(), "private-mount-marker {", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if _, ok := panics.Ref(err); !ok {
		t.Fatalf("expected panic reference, got %v", err)
	}
	if strings.Contains(err.Error(), "private-mount-marker") {
		t.Fatal("mount panic leaked input")
	}
}
