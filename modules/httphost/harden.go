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
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/core/panics/httpguard"
	"github.com/hollis-labs/libs/security/redact"

	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/forwarded"
	"github.com/hollis-labs/substrate/module"
)

// DefaultMaxBodyBytes is HardenOptions.MaxBodyBytes when it is zero.
const DefaultMaxBodyBytes = 8 << 20

// HardenOptions configure Harden. Each check is off unless set here or by its config key (shown
// in brackets); a field set here wins over its key.
type HardenOptions struct {
	// CheckHost refuses (403 host_not_allowed) a request whose Host is not loopback (localhost,
	// *.localhost, 127.0.0.0/8, ::1) or in AllowedHosts, against DNS rebinding. A non-empty
	// AllowedHosts turns it on. An entry is a host name as clients send it in Host, served to any
	// peer (what #23 called direct_hosts); one without a port matches any port. An empty entry, a
	// wildcard, a scheme, a path, user info or a space is an error. It never applies to a request
	// accepted on a unix-socket listener: peer credentials are that listener's caller check.
	// [httphost.check_host, httphost.allowed_hosts]
	CheckHost    bool
	AllowedHosts []string
	// CheckOrigin refuses (403 origin_not_allowed) a write (any method but GET, HEAD, OPTIONS)
	// whose Origin header is present and is neither the request's own host nor in AllowedOrigins
	// ("https://app.example"). A request with no Origin is not a browser cross-site write and
	// passes. A non-empty AllowedOrigins turns it on. [httphost.check_origin, httphost.allowed_origins]
	CheckOrigin    bool
	AllowedOrigins []string
	// TrustedProxy makes Scheme believe X-Forwarded-Proto from a local TLS proxy: exactly one
	// value, from a loopback peer (package forwarded, the rule identity uses too). It is the
	// identity module's switch,
	// and under Harden an X-Forwarded-Proto that does not meet the rule is removed before the
	// handler, identity included, sees it. Forwarded, X-Forwarded-For, X-Forwarded-Host and
	// X-Real-IP are always removed (when Handler turned Harden on from config alone, only with a
	// Host check or identity.trusted_proxy). [identity.trusted_proxy]
	TrustedProxy bool
	// MaxBodyBytes bounds a request body: a declared Content-Length above it is refused 413
	// body_too_large before the handler runs, and reading past it fails in the handler. Zero is
	// DefaultMaxBodyBytes when Harden is called (or Options.Harden set) in code; when Handler
	// applies Harden only because a config key is set, zero is no limit unless max_body_bytes is
	// configured, so turning on, say, the access log changes no request. Negative is no limit.
	// [httphost.max_body_bytes]
	MaxBodyBytes int64
	// AccessLog writes one line per request to Logger: method, the path (through redact.Text),
	// the query parameter NAMES (never their values), status, bytes, duration and remote address.
	// No header is logged, so Authorization and cookies never are. It changes no request.
	// [httphost.access_log]
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
// connection; the panic is reported to Logger, redacted), the forwarding headers, the Host
// check, the Origin check and the body limit. Called in code, panic recovery and the
// forwarding-header rule are always on; the rest follow o and, when app is not nil, the config
// keys (see HardenOptions). When Handler turns it on from config alone, only what is configured
// runs (no panic guard, no default limit), so a key changes nothing beyond itself. It
// serves httphost.Handler's result (Handler applies it itself when Options.Harden is set or an
// httphost hardening key is configured) or an app's own handler. A bad allowed_hosts entry is an
// error naming it.
//
// A raw handler's clients see Harden's refusals in the flat shape unless ErrorBody is set.
func Harden(app *module.App, h http.Handler, o HardenOptions) (http.Handler, error) {
	return harden(app, h, o, true)
}

// harden builds the chain. explicit is false when Handler turned Harden on only because a
// config key is set: then only what is configured runs, so turning on one key (the access log)
// changes no request: no panic guard, no default body limit, and the forwarding-header rule only
// with a Host check or identity.trusted_proxy.
func harden(app *module.App, h http.Handler, o HardenOptions, explicit bool) (http.Handler, error) {
	o, err := hardenConfig(app, o)
	if err != nil {
		return nil, err
	}
	hosts := map[string]bool{}
	for _, a := range o.AllowedHosts {
		if a == "" || strings.TrimSpace(a) != a || strings.ContainsAny(a, "*/\\@?# \t") {
			return nil, fmt.Errorf("httphost.allowed_hosts: %q is not a host name (no wildcard, scheme, path, user info or space)", a)
		}
		hosts[strings.ToLower(a)] = true
	}
	if len(o.AllowedHosts) > 0 {
		o.CheckHost = true
	}
	origins := map[string]bool{}
	for _, a := range o.AllowedOrigins {
		origins[strings.ToLower(strings.TrimRight(a, "/"))] = true
	}
	if len(o.AllowedOrigins) > 0 {
		o.CheckOrigin = true
	}
	if o.MaxBodyBytes == 0 && explicit {
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
			if !unixConn(r) && !hostAllowed(r.Host, hosts) {
				o.ErrorBody(w, r, http.StatusForbidden, "host_not_allowed", "the request's Host is not allowed")
				return
			}
			inner.ServeHTTP(w, r)
		})
	}
	if explicit || o.CheckHost || o.TrustedProxy {
		inner, trusted := next, o.TrustedProxy
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.Clone(r.Context())
			for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
				r.Header.Del(name)
			}
			https := trusted && forwarded.HTTPS(r)
			if !https {
				r.Header.Del("X-Forwarded-Proto")
			}
			inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), forwardedHTTPSKey{}, https)))
		})
	}
	logger := o.Logger
	guard := panics.New(panics.Config{
		Redact: redact.Text,
		Reporter: panics.ReporterFunc(func(rec panics.Record) {
			logger.Error("panic recovered", "ref", rec.Ref, "where", rec.Where, "value", rec.Value, "stack", rec.Stack)
		}),
	})
	if explicit {
		next = httpguard.Handler(next, httpguard.WithGuard(guard), httpguard.WithResponder(func(w http.ResponseWriter, r *http.Request, pe *panics.Error) {
			o.ErrorBody(w, r, http.StatusInternalServerError, "internal", pe.Error())
		}))
	}
	if o.AccessLog {
		next = accessLog(logger, next)
	}
	return next, nil
}

type forwardedHTTPSKey struct{}

// Scheme is "https" when r arrived over TLS or, under Harden with TrustedProxy, through a local
// TLS proxy (one X-Forwarded-Proto: https from a loopback peer); otherwise "http".
func Scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if https, _ := r.Context().Value(forwardedHTTPSKey{}).(bool); https {
		return "https"
	}
	return "http"
}

// unixConn reports whether the request was accepted on a unix-socket listener, from the
// connection (the server's local address), never from a header.
func unixConn(r *http.Request) bool {
	a, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return a != nil && (a.Network() == "unix" || a.Network() == "unixpacket")
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
// allowed list. "null" and unparsable origins are refused. Like identity's same-origin check,
// the own-host comparison ignores the scheme.
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

// hardenKeys are the httphost config keys that turn Handler's hardening on.
var hardenKeys = []string{"check_host", "allowed_hosts", "check_origin", "allowed_origins", "max_body_bytes", "access_log"}

// hardenConfig fills the zero fields of o from config.
func hardenConfig(app *module.App, o HardenOptions) (HardenOptions, error) {
	if app == nil {
		return o, nil
	}
	cfg, _ := app.Config["httphost"].(map[string]any)
	if b, ok := cfg["check_host"].(bool); ok && !o.CheckHost {
		o.CheckHost = b
	}
	if len(o.AllowedHosts) == 0 {
		l, err := strList(cfg, "allowed_hosts")
		if err != nil {
			return o, err
		}
		o.AllowedHosts = l
	}
	if b, ok := cfg["check_origin"].(bool); ok && !o.CheckOrigin {
		o.CheckOrigin = b
	}
	if len(o.AllowedOrigins) == 0 {
		l, err := strList(cfg, "allowed_origins")
		if err != nil {
			return o, err
		}
		o.AllowedOrigins = l
	}
	if id, _ := app.Config["identity"].(map[string]any); !o.TrustedProxy {
		o.TrustedProxy, _ = id["trusted_proxy"].(bool)
	}
	if n, ok := cfg["max_body_bytes"].(float64); ok && o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = int64(n)
	}
	if b, ok := cfg["access_log"].(bool); ok && !o.AccessLog {
		o.AccessLog = b
	}
	return o, nil
}

// hardenConfigured reports whether any hardening key is set under httphost:.
func hardenConfigured(app *module.App) bool {
	cfg, _ := app.Config["httphost"].(map[string]any)
	for _, k := range hardenKeys {
		if _, ok := cfg[k]; ok {
			return true
		}
	}
	return false
}

func strList(cfg map[string]any, key string) ([]string, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil, nil
	}
	var out []string
	switch l := raw.(type) {
	case []any:
		for _, e := range l {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("httphost.%s: entries must be strings, got %T", key, e)
			}
			out = append(out, s)
		}
	case []string:
		out = append(out, l...)
	default:
		return nil, fmt.Errorf("httphost.%s must be a list, got %T", key, raw)
	}
	return out, nil
}

// accessLog writes one line per request. It never logs a header or a query value.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logWriter{ResponseWriter: w}
		path, params := safeTarget(r.URL)
		defer func() {
			status := lw.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http request", "method", r.Method, "path", path, "params", params,
				"status", status, "bytes", lw.bytes, "duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr)
		}()
		next.ServeHTTP(lw, r)
	})
}

// safeTarget is the path through redact.Text and the sorted query parameter names (no values;
// a name is itself passed through redact.Text).
func safeTarget(u *url.URL) (path, params string) {
	var names []string
	for k := range u.Query() {
		names = append(names, redact.Text(k))
	}
	slices.Sort(names)
	return redact.Text(u.EscapedPath()), strings.Join(names, ",")
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
