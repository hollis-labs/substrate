package httphost

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/clientaddr"
)

// Host check. The backend binds loopback; a browser pointed at it from another site
// (DNS rebinding) arrives with a Host the app does not serve and is refused before any
// handler sees it. Served Hosts are loopback names and addresses, plus the operator's
// httphost.allowed_hosts for a local TLS proxy that forwards a public name.

const (
	codeInvalidHost    = "invalid_host"
	codeUntrustedProxy = "untrusted_proxy"
)

// parseHostList reads one httphost list key (allowed_hosts or direct_hosts): host names
// exactly as a client sends them in Host (a port only when the client sends one). Entries
// are lower-cased. An empty entry, a wildcard, or anything with a scheme, path, user info
// or space is an error, so a typo fails closed at start.
func parseHostList(cfg map[string]any, key string) ([]string, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil, nil
	}
	var in []string
	switch v := raw.(type) {
	case []string:
		in = v
	case []any:
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("httphost.%s: entries must be strings, got %T", key, e)
			}
			in = append(in, s)
		}
	default:
		return nil, fmt.Errorf("httphost.%s must be a list of host names, got %T", key, raw)
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s, "*/\\@?# \t") {
			return nil, fmt.Errorf("httphost.%s: %q is not a host name (no wildcard, scheme, path, user info or space)", key, s)
		}
		out = append(out, strings.ToLower(s))
	}
	return out, nil
}

// parseHosts reads httphost.allowed_hosts (names served only through a local TLS proxy)
// and httphost.direct_hosts (names served directly on the bound address). A name in both
// is an error: the two mean different trust.
func parseHosts(cfg map[string]any) (allowed, direct []string, err error) {
	if allowed, err = parseHostList(cfg, "allowed_hosts"); err != nil {
		return nil, nil, err
	}
	if direct, err = parseHostList(cfg, "direct_hosts"); err != nil {
		return nil, nil, err
	}
	for _, d := range direct {
		if slices.Contains(allowed, d) {
			return nil, nil, fmt.Errorf("httphost.direct_hosts: %q is also in httphost.allowed_hosts; a name is served either through a local proxy or directly, not both", d)
		}
	}
	return allowed, direct, nil
}

// hostGuard refuses requests for hosts that are not served and strips forwarding
// headers. allowed holds lower-cased names.
func hostGuard(allowed []string, next http.Handler) http.Handler {
	return hostGuardDirect(allowed, nil, next)
}

// hostGuardDirect is hostGuard with direct, the names served on the bound address to any
// peer. A direct name is believed for nothing: no forwarding header is kept (X-Forwarded-Proto
// included, so identity's trusted_proxy never trusts it) and the client address is the peer.
func hostGuardDirect(allowed, direct []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set on refusals too: a refusal page must not be framed or leak a referrer.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		proxied := false
		switch {
		case isLoopbackHost(r.Host):
		case slices.Contains(direct, strings.ToLower(r.Host)):
			// Served as is; proxied stays false so every forwarding header is stripped.
		case slices.Contains(allowed, strings.ToLower(r.Host)):
			values := r.Header.Values("X-Forwarded-Proto")
			if !loopbackPeer(r.RemoteAddr) || len(values) != 1 || values[0] != "https" {
				slog.Debug("httphost: refused a listed host without a trusted https proxy", "peer", r.RemoteAddr)
				apierr.Write(w, http.StatusForbidden, codeUntrustedProxy,
					"this host is served only through the local TLS proxy", nil)
				return
			}
			proxied = true
		default:
			slog.Debug("httphost: refused a host that is not served", "host", r.Host)
			apierr.Write(w, http.StatusForbidden, codeInvalidHost, "this host is not served", nil)
			return
		}
		// The client address is read before the forwarding headers go, and only for a
		// request the check just validated as arriving through the local proxy.
		var client string
		if proxied {
			client = forwardedClient(r.Header.Values("X-Forwarded-For"))
		}
		r = r.Clone(r.Context())
		for _, name := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "X-Real-IP"} {
			r.Header.Del(name)
		}
		// X-Forwarded-Proto is the one hint the identity module may use (trusted_proxy);
		// it survives only when just validated, so a loopback request cannot claim https.
		if !proxied {
			r.Header.Del("X-Forwarded-Proto")
		}
		if client != "" {
			r = r.WithContext(clientaddr.With(r.Context(), client))
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedClient returns the client address a validated local proxy reported, or ""
// when there is none to believe. It takes the LAST hop: a proxy that appends the peer
// it saw to X-Forwarded-For ($proxy_add_x_forwarded_for, Caddy and Traefik defaults)
// writes the last entry itself, and everything before it is whatever the client
// sent, so the first hop is attacker-chosen. A proxy that overwrites the header
// gives a single entry, the same one. The hop must be a bare IP (a port, brackets
// and a zone are not accepted); anything else gives "" and the caller falls back to
// the peer address.
func forwardedClient(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	hop := strings.TrimSpace(parts[len(parts)-1])
	ip := net.ParseIP(hop)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// loopbackAddr reports whether a listen address binds only loopback.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && host != "" && isLoopbackHost(host)
}

func loopbackPeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackHost(host string) bool {
	name := host
	if strings.Contains(name, ":") {
		var err error
		name, _, err = net.SplitHostPort(name)
		if err != nil {
			// A bare IPv6 literal without brackets or port, such as ::1.
			name = strings.Trim(host, "[]")
		}
	}
	name = strings.Trim(name, "[]")
	ip := net.ParseIP(name)
	return name == "localhost" || (ip != nil && ip.IsLoopback())
}
