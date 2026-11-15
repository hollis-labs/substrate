package httphost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/core/panics/httpguard"
	"github.com/hollis-labs/libs/security/redact"

	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/module"
)

// DefaultMaxBodyBytes is HardenOptions.MaxBodyBytes when it is zero.
const DefaultMaxBodyBytes = 8 << 20

// HardenOptions configure Harden. Each check is off unless set here or by its config key under
// httphost: (shown in brackets); a field set here wins over its key.
type HardenOptions struct {
	// CheckHost refuses (403 host_not_allowed) a request whose Host is not loopback (localhost,
	// *.localhost, 127.0.0.0/8, ::1) or in AllowedHosts, against DNS rebinding. A non-empty
	// AllowedHosts turns it on. An entry without a port matches any port. [check_host, allowed_hosts]
	CheckHost    bool
	AllowedHosts []string
	// CheckOrigin refuses (403 origin_not_allowed) a write (any method but GET, HEAD, OPTIONS)
	// whose Origin header is present and is neither the request's own host nor in AllowedOrigins
	// ("https://app.example"). A request with no Origin is not a browser cross-site write and
	// passes. A non-empty AllowedOrigins turns it on. [check_origin, allowed_origins]
	CheckOrigin    bool
	AllowedOrigins []string
	// TrustForwardedProto makes Scheme believe a single X-Forwarded-Proto, for a local TLS proxy
	// that overwrites it. Off, the header is ignored. [trust_forwarded_proto]
	TrustForwardedProto bool
	// MaxBodyBytes bounds a request body: a declared Content-Length above it is refused 413
	// body_too_large before the handler runs, and reading past it fails in the handler. Zero is
	// DefaultMaxBodyBytes; negative is no limit. [max_body_bytes]
	MaxBodyBytes int64
	// AccessLog writes one line per request to Logger: method, the path with
	// credential-like query values redacted, status, bytes, duration and remote address. No
	// header is logged, so Authorization and cookies never are. [access_log]
	AccessLog bool
	// Logger receives the access log and recovered panics (ref, where, redacted value and stack).
	// Nil is slog.Default().
	Logger *slog.Logger
	// ErrorBody writes Harden's own refusals and the 500 for a recovered panic, for a handler whose
	// clients expect another envelope. Nil is the flat apierr body. The 500's message carries
	// only the panic ref.
	ErrorBody func(w http.ResponseWriter, r *http.Request, status int, code, message string)
}

// Harden wraps h in the request hardening chain, outermost first: the access log, panic
// recovery (a panicking handler answers 500 internal with a ref instead of dropping the
// connection; the panic is reported to Logger, redacted), the Host check, the Origin check and
// the body limit. Panic recovery is always on; the rest follow o and, when app is not nil, the
// config keys under httphost: (see HardenOptions). It serves httphost.Handler's result (Handler
// applies it itself when Options.Harden is set or a key is configured) or an app's own handler.
//
// A raw handler's clients see Harden's refusals in the flat shape unless ErrorBody is set.
func Harden(app *module.App, h http.Handler, o HardenOptions) http.Handler {
	o = hardenConfig(app, o)
	if len(o.AllowedHosts) > 0 {
		o.CheckHost = true
	}
	if len(o.AllowedOrigins) > 0 {
		o.CheckOrigin = true
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.ErrorBody == nil {
		o.ErrorBody = func(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
			apierr.Write(w, status, code, message, nil)
		}
	}
	hosts := map[string]bool{}
	for _, a := range o.AllowedHosts {
		hosts[strings.ToLower(a)] = true
	}
	origins := map[string]bool{}
	for _, a := range o.AllowedOrigins {
		origins[strings.ToLower(strings.TrimRight(a, "/"))] = true
	}
	trust := o.TrustForwardedProto

	next := h
	if o.MaxBodyBytes > 0 {
		inner, limit := next, o.MaxBodyBytes
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				o.ErrorBody(w, r, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf("request body is larger than %d bytes", limit))
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			inner.ServeHTTP(w, r)
		})
	}
	if o.CheckOrigin {
		inner := next
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !originAllowed(r, origins) {
				o.ErrorBody(w, r, http.StatusForbidden, "origin_not_allowed", "the request's Origin is not allowed")
				return
			}
			inner.ServeHTTP(w, r)
		})
	}
	if o.CheckHost {
		inner := next
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hostAllowed(r.Host, hosts) {
				o.ErrorBody(w, r, http.StatusForbidden, "host_not_allowed", "the request's Host is not allowed")
				return
			}
			inner.ServeHTTP(w, r)
		})
	}
	{
		inner := next
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), trustProtoKey{}, trust)))
		})
	}
	logger := o.Logger
	guard := panics.New(panics.Config{
		Redact: redact.Text,
		Reporter: panics.ReporterFunc(func(rec panics.Record) {
			logger.Error("panic recovered", "ref", rec.Ref, "where", rec.Where, "value", rec.Value, "stack", rec.Stack)
		}),
	})
	next = httpguard.Handler(next, httpguard.WithGuard(guard), httpguard.WithResponder(func(w http.ResponseWriter, r *http.Request, pe *panics.Error) {
		o.ErrorBody(w, r, http.StatusInternalServerError, "internal", pe.Error())
	}))
	if o.AccessLog {
		next = accessLog(logger, next)
	}
	return next
}

type trustProtoKey struct{}

// Scheme is "https" when r arrived over TLS or, under Harden with TrustForwardedProto, when a
// single X-Forwarded-Proto says https; otherwise "http".
func Scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if trust, _ := r.Context().Value(trustProtoKey{}).(bool); trust {
		if vals := r.Header.Values("X-Forwarded-Proto"); len(vals) == 1 && strings.EqualFold(strings.TrimSpace(vals[0]), "https") {
			return "https"
		}
	}
	return "http"
}

// hostAllowed accepts loopback names and addresses and the allowed list (exact host:port, or a
// port-less entry for any port).
func hostAllowed(hostport string, allowed map[string]bool) bool {
	hostport = strings.ToLower(hostport)
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return allowed[hostport] || allowed[host]
}

// originAllowed passes safe methods, a request without Origin, the request's own host and the
// allowed list. "null" and unparsable origins are refused.
func originAllowed(r *http.Request, allowed map[string]bool) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	if allowed[o] {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// hardenConfig fills the zero fields of o from config httphost.*.
func hardenConfig(app *module.App, o HardenOptions) HardenOptions {
	if app == nil {
		return o
	}
	cfg, _ := app.Config["httphost"].(map[string]any)
	if b, ok := cfg["check_host"].(bool); ok && !o.CheckHost {
		o.CheckHost = b
	}
	if len(o.AllowedHosts) == 0 {
		o.AllowedHosts = strList(cfg["allowed_hosts"])
	}
	if b, ok := cfg["check_origin"].(bool); ok && !o.CheckOrigin {
		o.CheckOrigin = b
	}
	if len(o.AllowedOrigins) == 0 {
		o.AllowedOrigins = strList(cfg["allowed_origins"])
	}
	if b, ok := cfg["trust_forwarded_proto"].(bool); ok && !o.TrustForwardedProto {
		o.TrustForwardedProto = b
	}
	if n, ok := cfg["max_body_bytes"].(float64); ok && o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = int64(n)
	}
	if b, ok := cfg["access_log"].(bool); ok && !o.AccessLog {
		o.AccessLog = b
	}
	return o
}

// hardenConfigured reports whether any hardening key is set under httphost:.
func hardenConfigured(app *module.App) bool {
	cfg, _ := app.Config["httphost"].(map[string]any)
	for _, k := range []string{"check_host", "allowed_hosts", "check_origin", "allowed_origins", "trust_forwarded_proto", "max_body_bytes", "access_log"} {
		if _, ok := cfg[k]; ok {
			return true
		}
	}
	return false
}

func strList(v any) []string {
	var out []string
	switch l := v.(type) {
	case []any:
		for _, e := range l {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, l...)
	}
	return out
}

// accessLog writes one line per request. It never logs a header; the query's values are
// redacted for credential-like names, and the path and query pass through redact.Text.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logWriter{ResponseWriter: w}
		path := safeTarget(r.URL)
		defer func() {
			status := lw.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http request", "method", r.Method, "path", path,
				"status", status, "bytes", lw.bytes, "duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr)
		}()
		next.ServeHTTP(lw, r)
	})
}

// safeTarget is the path and query with credential-like query values replaced.
func safeTarget(u *url.URL) string {
	out := u.EscapedPath()
	if u.RawQuery != "" {
		q := u.Query()
		for k, vs := range q {
			if redact.SensitiveName(k) {
				for i := range vs {
					vs[i] = "[REDACTED]"
				}
			}
		}
		out += "?" + q.Encode()
	}
	return redact.Text(out)
}

// logWriter records the status and bytes, and keeps Flush, Hijack and Unwrap working.
type logWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (l *logWriter) WriteHeader(code int) {
	if l.status == 0 {
		l.status = code
	}
	l.ResponseWriter.WriteHeader(code)
}

func (l *logWriter) Write(b []byte) (int, error) {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	n, err := l.ResponseWriter.Write(b)
	l.bytes += int64(n)
	return n, err
}

func (l *logWriter) Flush() {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (l *logWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := l.ResponseWriter.(http.Hijacker); ok {
		if l.status == 0 {
			l.status = http.StatusSwitchingProtocols
		}
		return hj.Hijack()
	}
	return nil, nil, errors.ErrUnsupported
}

func (l *logWriter) Unwrap() http.ResponseWriter { return l.ResponseWriter }
