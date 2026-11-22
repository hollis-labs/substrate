// Package forwarded is the one trusted-proxy rule Substrate applies to X-Forwarded-Proto, shared
// by the identity module (identity.trusted_proxy / trusted_proxies: secure cookies) and
// httphost.Harden (Scheme). A TLS proxy is believed only from the peers it is configured for:
// exactly one X-Forwarded-Proto, "https", from an address in the trusted prefixes (HTTPSFrom);
// HTTPS is the local case, a loopback peer.
package forwarded

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Loopback is the local-proxy case: a proxy on the same host (tailscale serve, a Cloudflare
// tunnel, Caddy on loopback).
var Loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// HTTPSFrom reports whether r came through a TLS proxy at one of the trusted prefixes: exactly
// one X-Forwarded-Proto whose value is https, from a peer (r.RemoteAddr) inside trusted. An
// empty trusted list believes nobody.
func HTTPSFrom(r *http.Request, trusted []netip.Prefix) bool {
	vals := r.Header.Values("X-Forwarded-Proto")
	if len(trusted) == 0 || len(vals) != 1 || !strings.EqualFold(strings.TrimSpace(vals[0]), "https") {
		return false
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := ap.Addr().Unmap()
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ParsePrefixes reads CIDRs ("192.0.2.0/24", "127.0.0.1/32") or plain addresses (taken as a
// single host).
func ParsePrefixes(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, err
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

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
