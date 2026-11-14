package database

import (
	"net/http"

	"github.com/hollis-labs/substrate/apierr"
)

// Gate is middleware for routes that need a named store: while it is not ready
// it answers 503 {"code":"database_unavailable"} with Retry-After and does not
// call next. Put it in front of data endpoints and leave sign-in, settings and
// /healthz outside it.
func Gate(h *Handle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !h.Ready() {
				WriteUnavailable(w, h)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteUnavailable writes the standard 503 for a handler that called Get and
// got ErrUnavailable.
func WriteUnavailable(w http.ResponseWriter, h *Handle) {
	w.Header().Set("Retry-After", "5")
	apierr.Write(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable",
		map[string]any{"store": h.Name()})
}
