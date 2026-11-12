// Package clientaddr carries the address of the caller as the host validated it.
//
// The httphost host check strips every forwarding header before routing, so a
// handler cannot read the client behind a local TLS proxy from X-Forwarded-For.
// The check records the one address it was willing to believe (see httphost) in the
// request context, and From returns it. Code that keys on the caller's address
// (a rate limiter, an audit line) calls From, never reads RemoteAddr or a header.
package clientaddr

import (
	"context"
	"net"
	"net/http"
)

type key struct{}

// With returns a context that carries addr, a bare IP with no port.
func With(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, key{}, addr)
}

// From returns the validated client address when the host check recorded one, and
// otherwise the host part of the connection's peer address (without the port).
func From(r *http.Request) string {
	if a, ok := r.Context().Value(key{}).(string); ok && a != "" {
		return a
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
