// Package apierr is the error convention for every route Substrate serves:
// a flat JSON body {code, message, details?}. The app-runtime API client reads
// `message` at the top level, so a nested body shows the user "Request failed".
package apierr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Body is the wire shape.
type Body struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Write sends an error response in the convention.
func Write(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{Code: code, Message: message, Details: details})
}

// Normalize rewrites error responses that use libs' nested shape
// {"error":{"code","message"}} (the auth guard's) into the flat convention, and
// leaves everything else untouched, including streaming responses. Handlers
// should use Write; this exists for code Substrate wraps but does not own.
func Normalize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nw := &normWriter{ResponseWriter: w}
		next.ServeHTTP(nw, r)
		nw.finish()
	})
}

type normWriter struct {
	http.ResponseWriter
	status    int
	buffering bool
	buf       bytes.Buffer
}

func (n *normWriter) WriteHeader(code int) {
	if n.status != 0 {
		return
	}
	n.status = code
	ct := n.Header().Get("Content-Type")
	if code >= 400 && strings.HasPrefix(ct, "application/json") {
		n.buffering = true
		return
	}
	n.ResponseWriter.WriteHeader(code)
}

func (n *normWriter) Write(b []byte) (int, error) {
	if n.status == 0 {
		n.WriteHeader(http.StatusOK)
	}
	if n.buffering {
		return n.buf.Write(b)
	}
	return n.ResponseWriter.Write(b)
}

// Flush keeps SSE and other streaming handlers working through the wrapper.
func (n *normWriter) Flush() {
	if n.buffering {
		return
	}
	if f, ok := n.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.NewResponseController reach the real writer.
func (n *normWriter) Unwrap() http.ResponseWriter { return n.ResponseWriter }

func (n *normWriter) finish() {
	if !n.buffering {
		return
	}
	out := n.buf.Bytes()
	var nested struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if json.Unmarshal(out, &nested) == nil && nested.Code == "" && nested.Error != nil && nested.Error.Code != "" {
		msg := nested.Error.Message
		if msg == "" {
			msg = http.StatusText(n.status)
		}
		out, _ = json.Marshal(Body{Code: nested.Error.Code, Message: msg})
		out = append(out, '\n')
	}
	n.Header().Set("Content-Length", strconv.Itoa(len(out)))
	n.ResponseWriter.WriteHeader(n.status)
	_, _ = n.ResponseWriter.Write(out)
}
