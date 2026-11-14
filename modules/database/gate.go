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
	return GateWith(h, func(w http.ResponseWriter, _ *http.Request, _ Status) { WriteUnavailable(w, h) })
}

// GateWith is Gate with the refusal written by deny, for a route whose clients
// expect their own error envelope (a protocol-fixed API). deny is called with
// the store's Status and must write the response; set Retry-After as Gate does.
// A nil Handle is never ready, so the gate refuses.
func GateWith(h *Handle, deny func(w http.ResponseWriter, r *http.Request, s Status)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !h.Ready() {
				deny(w, r, h.Status())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteUnavailable writes the standard 503 for a handler that called Get and
// got ErrUnavailable.
func WriteUnavailable(w http.ResponseWriter, h *Handle) { // h may be nil
	w.Header().Set("Retry-After", "5")
	apierr.Write(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable",
		map[string]any{"store": h.Name()})
}
