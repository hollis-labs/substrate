package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/genui"
)

// ScopeResolver maps a verified credential to stable actor/namespace identity.
// It must consult the host's current enrollment, including alias revocation.
type ScopeResolver interface {
	Resolve(context.Context, libauth.Principal) (Scope, error)
}

type HTTP struct {
	service  *Service
	resolver ScopeResolver
	prefix   string
}

func NewHTTP(service *Service, resolver ScopeResolver, scopePrefix string) (*HTTP, error) {
	if service == nil || resolver == nil || strings.TrimSpace(scopePrefix) == "" {
		return nil, ErrUnavailable
	}
	return &HTTP{service, resolver, scopePrefix}, nil
}

// Handlers are mounted by the application's existing HTTP host. They do not
// open listeners. Each checks verified identity and its explicit scope even if
// an outer guard is permissive, off, or configured with a broader route scope.
func (h *HTTP) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"POST /api/genui/surfaces":          h.scoped("show", h.show),
		"POST /api/genui/actions":           h.scoped("act", h.submit),
		"GET /api/genui/actions/{actionID}": h.scoped("read", h.recover),
	}
}

type scopedHandler func(http.ResponseWriter, *http.Request, Scope)

func (h *HTTP) scoped(scope string, next scopedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := libauth.FromContext(r.Context())
		if !ok || !p.Verified || p.ID == "" || (!p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)) {
			apierr.Write(w, http.StatusUnauthorized, "unauthenticated", "Verified identity is required.", nil)
			return
		}
		if !p.HasScope(h.prefix + "." + scope) {
			apierr.Write(w, http.StatusForbidden, "forbidden", "Required scope is absent.", nil)
			return
		}
		ctx := r.Context()
		if !p.ExpiresAt.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, p.ExpiresAt)
			defer cancel()
		}
		resolved, err := h.resolver.Resolve(ctx, p)
		if err != nil || !validScope(resolved) {
			apierr.Write(w, http.StatusForbidden, "forbidden", "Actor mapping is unavailable.", nil)
			return
		}
		next(w, r.WithContext(ctx), resolved)
	})
}
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.URL.RawQuery != "" {
		return nil, ErrInvalid
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, ErrInvalid
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, genui.MaxActionBytes))
	if err != nil {
		return nil, ErrInvalid
	}
	return b, nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	apierr.Write(w, status, code, "GenUI request could not be completed.", nil)
}
func (h *HTTP) show(w http.ResponseWriter, r *http.Request, scope Scope) {
	b, err := readBody(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	b, err = genui.CanonicalJSON(b)
	if err != nil {
		writeError(w, ErrInvalid)
		return
	}
	var q ShowRequest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil {
		writeError(w, ErrInvalid)
		return
	}
	out, err := h.service.Show(r.Context(), scope, q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *HTTP) submit(w http.ResponseWriter, r *http.Request, scope Scope) {
	b, err := readBody(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := h.service.Submit(r.Context(), scope, b)
	if err != nil && out.AgentFunctionResponse.FunctionCallID == "" {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, out)
}
func (h *HTTP) recover(w http.ResponseWriter, r *http.Request, scope Scope) {
	q, err := jsonQuery(r)
	if err != nil {
		writeError(w, err)
		return
	}
	out, err := h.service.Recover(r.Context(), scope, r.PathValue("actionID"), q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func jsonQuery(r *http.Request) (string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 4096 || len(q) != 1 || len(q["functionCallId"]) != 1 || q.Get("functionCallId") == "" || r.ContentLength > 0 {
		return "", ErrInvalid
	}
	if r.Body != nil {
		b, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
		if readErr != nil || len(b) != 0 {
			return "", ErrInvalid
		}
	}
	return q.Get("functionCallId"), nil
}
