package httphost_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// The OpenAI-dialect error, with every field a client reads and some the flattener would drop.
const openAIError = `{"error":{"message":"The model 'x' does not exist","type":"invalid_request_error","param":null,"code":"model_not_found"}}`

func upstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ok"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"1","error":null}`))
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			for _, e := range []string{"data: one\n\n", "data: two\n\n"} {
				_, _ = w.Write([]byte(e))
				w.(http.Flusher).Flush()
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Upstream", "yes")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(openAIError))
		}
	})
}

const rawKey = "raw-key"

// rawApp is an enforcing app with a relay under /api/relay/ (raw), an ordinary route under /api/own/,
// an unmapped route under /api/loose/ (raw) and mounts outside /api.
func rawApp(t *testing.T, extra ...string) (*module.App, httphost.Options) {
	t.Helper()
	cfg := `{"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/a","hash":"` + libauth.HashKey(rawKey) + `","scopes":["t.relay"]},` +
		`{"id":"svc/b","hash":"` + libauth.HashKey("other") + `","scopes":["t.other"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	tbl := auth.ScopeTable(run.App)
	tbl.Route("*", "/api/relay/*", "t.relay")
	tbl.Route("*", "/api/own/*", "t.relay")
	tbl.Route("*", "/rawmount/*", "t.relay")
	tbl.Route("*", "/plainmount/*", "t.relay")
	// /api/loose/ has no scope on purpose.
	api := http.NewServeMux()
	for _, p := range []string{"/api/relay/", "/api/own/", "/api/loose/"} {
		api.Handle(p, upstream())
	}
	return run.App, httphost.Options{
		Routes:      api,
		RawPrefixes: append([]string{"/api/relay/", "/api/loose/"}, extra...),
		Mounts: []httphost.Mount{
			{Pattern: "/rawmount/", Handler: upstream(), Raw: true},
			{Pattern: "/plainmount/", Handler: upstream()},
			{Pattern: "GET /rawopen/", Handler: upstream(), Raw: true, Open: true},
			{Pattern: "GET /plainopen/", Handler: upstream(), Open: true},
		},
	}
}

func rawHandler(t *testing.T) http.Handler {
	t.Helper()
	app, o := rawApp(t)
	h, err := httphost.Handler(app, o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestRawPassesTheUpstreamErrorThroughByteForByte(t *testing.T) {
	h := rawHandler(t)
	for _, path := range []string{"/api/relay/v1/chat/completions", "/rawmount/v1/x", "/rawopen/v1/x"} {
		code, body := status(h, "POST", path, rawKey)
		if path == "/rawopen/v1/x" {
			code, body = status(h, "GET", path, "")
		}
		if code != 404 || body != openAIError {
			t.Errorf("%s: %d %q, want the upstream body untouched", path, code, body)
		}
	}
	r := httptest.NewRequest("POST", "/api/relay/v1/x", nil)
	r.Header.Set("Authorization", "Bearer "+rawKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-Upstream") != "yes" || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("upstream headers lost: %v", w.Header())
	}
}

func TestNormalPathsStillFlattenTheNestedError(t *testing.T) {
	h := rawHandler(t)
	for _, c := range []struct{ method, path, bearer string }{
		{"POST", "/api/own/v1/x", rawKey}, {"POST", "/plainmount/x", rawKey}, {"GET", "/plainopen/x", ""},
	} {
		code, body := status(h, c.method, c.path, c.bearer)
		if code != 404 || !strings.Contains(body, `"code":"model_not_found"`) || strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %q, want the flat body", c.path, code, body)
		}
	}
	// A prefix is a prefix of path segments, not of a name: /api/relayx/ is not raw.
	app, o := rawApp(t)
	o.Routes.Handle("/api/relayx/", upstream())
	auth.ScopeTable(app).Route("*", "/api/relayx/*", "t.relay")
	h2, err := httphost.Handler(app, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, body := status(h2, "POST", "/api/relayx/a", rawKey); strings.Contains(body, `"error"`) {
		t.Errorf("a sibling path was served raw: %s", body)
	}
}

func isFlat(body string) bool {
	return strings.Contains(body, `"code":`) && strings.Contains(body, `"message":`) && !strings.Contains(body, `"error"`)
}

func TestFrameworkRefusalsOnARawPathStayFlat(t *testing.T) {
	h := rawHandler(t)
	for name, c := range map[string]struct {
		method, path, bearer string
		want                 int
	}{
		"no credential, raw prefix": {"POST", "/api/relay/v1/x", "", 401},
		"no credential, raw mount":  {"POST", "/rawmount/x", "", 401},
		"wrong scope, raw prefix":   {"POST", "/api/relay/v1/x", "other", 403},
		"wrong scope, raw mount":    {"POST", "/rawmount/x", "other", 403},
		"unmapped route":            {"POST", "/api/loose/x", rawKey, 403},
	} {
		code, body := status(h, c.method, c.path, c.bearer)
		if code != c.want || !isFlat(body) {
			t.Errorf("%s: %d %q, want %d in the flat shape", name, code, body, c.want)
		}
	}
}

func TestSuccessAndStreamsAreUntouchedInBothModes(t *testing.T) {
	h := rawHandler(t)
	for _, base := range []string{"/api/relay", "/api/own", "/rawmount", "/plainmount"} {
		if code, body := status(h, "POST", base+"/ok", rawKey); code != 200 || body != `{"id":"1","error":null}` {
			t.Errorf("%s success: %d %q", base, code, body)
		}
		// The stream must reach the client flushed, chunk by chunk, through a real server.
		srv := httptest.NewServer(h)
		req, _ := http.NewRequest("POST", srv.URL+base+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(string(buf[:n]), "data: one\n\n") ||
			resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Errorf("%s stream: %d %q %v", base, resp.StatusCode, buf[:n], resp.Header)
		}
	}
}

func TestRawPrefixValidation(t *testing.T) {
	// A module-declared route the prefix may not cover or sit inside.
	mod := func(app *module.App) {
		app.Values[module.RoutesKey] = []module.Route{
			{Method: "GET", Path: "/api/identity/profile", Handler: ok("x")},
			{Method: "*", Path: "/api/mod/", Handler: ok("x")},
		}
	}
	for name, c := range map[string]struct {
		prefixes []string
		mounts   []httphost.Mount
		want     string
	}{
		"empty":                 {[]string{""}, nil, "empty"},
		"bare api":              {[]string{"/api/"}, nil, "all of /api/"},
		"no trailing slash":     {[]string{"/api/relay"}, nil, `"/api/relay"`},
		"relative":              {[]string{"api/relay/"}, nil, `"api/relay/"`},
		"dotdot":                {[]string{"/api/../x/"}, nil, `"/api/../x/"`},
		"duplicate":             {[]string{"/api/a/", "/api/a/"}, nil, "listed twice"},
		"outside api":           {[]string{"/elsewhere/"}, nil, `"/elsewhere/"`},
		"covers a module route": {[]string{"/api/identity/"}, nil, "/api/identity/profile"},
		"inside a module route": {[]string{"/api/mod/v1/"}, nil, "/api/mod/"},
	} {
		run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, `{"auth":{"mode":"off"}}`)
		if err != nil {
			t.Fatal(err)
		}
		mod(run.App)
		_, err = httphost.Handler(run.App, httphost.Options{RawPrefixes: c.prefixes, Mounts: c.mounts})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %s", name, err, c.want)
		}
		_ = run.Stop(context.Background())
	}
	// Accepted: under /api/, clear of module routes; and a prefix naming a mount.
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, `{"auth":{"mode":"off"}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	mod(run.App)
	if _, err := httphost.Handler(run.App, httphost.Options{
		RawPrefixes: []string{"/api/llm/", "/api/llm2/", "/relay/"},
		Mounts:      []httphost.Mount{{Pattern: "/relay/", Handler: ok("x")}},
	}); err != nil {
		t.Errorf("valid prefixes refused: %v", err)
	}
}
