package identity

import (
	"net/http"
	"net/url"
	"strings"
)

// Middleware is the browser half of sign-in. Put it outside the app's handler
// (outside httphost's, so it runs before the guard): a request that carries the
// session cookie and no Authorization header gets the cookie's value as its
// bearer, which the guard then verifies like any other credential.
//
// A write authenticated by the cookie also has to prove it came from the app's
// own pages: an Origin, when sent, must be this app's, and the request must
// carry an X-Requested-With header, which a cross-site form cannot set. Requests
// that bring their own Authorization header are not cookie-authenticated and are
// not subject to this.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(s.cfg.cookie); err == nil && strings.HasPrefix(c.Value, sessionPrefix) {
				if unsafeMethod(r.Method) && !s.sameOrigin(r) {
					http.Error(w, `{"code":"bad_origin","message":"the request did not come from this app"}`, http.StatusForbidden)
					return
				}
				if unsafeMethod(r.Method) && r.Header.Get("X-Requested-With") == "" {
					http.Error(w, `{"code":"csrf","message":"missing X-Requested-With"}`, http.StatusForbidden)
					return
				}
				r = r.Clone(r.Context())
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func unsafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin accepts a request with no Origin (not a browser cross-site write)
// or whose Origin is this app's own host or one configured.
func (s *Service) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.cfg.origins[strings.TrimRight(origin, "/")] {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func (s *Service) secureCookie(r *http.Request) bool {
	switch s.cfg.secure {
	case "always":
		return true
	case "never":
		return false
	}
	if r.TLS != nil {
		return true
	}
	if s.cfg.trustedProxy {
		vals := r.Header.Values("X-Forwarded-Proto")
		return len(vals) == 1 && strings.EqualFold(vals[0], "https")
	}
	return false
}
