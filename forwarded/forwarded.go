// Package forwarded is the one trusted-proxy rule Substrate applies to X-Forwarded-Proto, shared
// by the identity module (identity.trusted_proxy: secure cookies) and httphost.Harden (Scheme).
// A local TLS proxy is believed: exactly one X-Forwarded-Proto, "https", from a loopback peer.
// A proxy on another host is not supported; such an app sets identity.secure: always.
package forwarded

import (
	"net"
	"net/http"
	"strings"
)

// HTTPS reports whether r came through a local TLS proxy: exactly one X-Forwarded-Proto whose
// value is https, from a loopback peer (r.RemoteAddr). The caller decides whether a proxy is
// trusted at all.
func HTTPS(r *http.Request) bool {
	vals := r.Header.Values("X-Forwarded-Proto")
	if len(vals) != 1 || !strings.EqualFold(strings.TrimSpace(vals[0]), "https") {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
