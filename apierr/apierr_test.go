package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(h http.Handler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	return w
}

func TestNestedLibsBodyBecomesFlat(t *testing.T) {
	w := serve(Normalize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"route_not_mapped","message":"route has no scope"}}`))
	})))
	var b Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if w.Code != 403 || b.Code != "route_not_mapped" || b.Message != "route has no scope" {
		t.Fatalf("got %d %+v", w.Code, b)
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("nested key survived: %s", w.Body)
	}
}

func TestFlatBodyAndSuccessAreUntouched(t *testing.T) {
	w := serve(Normalize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		Write(w, 404, "not_found", "no such thing", map[string]string{"id": "7"})
	})))
	var b Body
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 404 || b.Code != "not_found" || b.Details == nil {
		t.Fatalf("flat body changed: %d %s", w.Code, w.Body)
	}
	ok := serve(Normalize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":"x","message":"y"}}`)) // a 200 that happens to carry the key
	})))
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"error"`) {
		t.Fatalf("a success response must not be rewritten: %d %s", ok.Code, ok.Body)
	}
}

func TestStreamingStillFlushes(t *testing.T) {
	w := serve(Normalize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})))
	if !w.Flushed || w.Body.String() != "data: 1\n\n" {
		t.Fatalf("flushed=%v body=%q", w.Flushed, w.Body)
	}
}
