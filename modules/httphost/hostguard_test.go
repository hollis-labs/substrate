package httphost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/clientaddr"
)

const proxied = "app.example.test"

// The boundary table, ported from the apps' hostguard copies: which hosts are served,
// when a listed name is believed, and what a downstream handler may still see.
func TestHostBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, host, peer, proto string
		listed                  bool
		accepted                bool
		keepsProto              bool
		wantCode                string // for a refusal
	}{
		{"localhost with port", "localhost:8080", "127.0.0.1:1234", "", true, true, false, ""},
		{"localhost no port", "localhost", "127.0.0.1:1234", "", false, true, false, ""},
		{"loopback ip", "127.0.0.1:8080", "127.0.0.1:1234", "", false, true, false, ""},
		{"loopback ip no port", "127.0.0.1", "127.0.0.1:1234", "", false, true, false, ""},
		{"other loopback v4", "127.0.0.2", "127.0.0.1:1234", "", false, true, false, ""},
		{"ipv6", "[::1]:8080", "[::1]:1234", "", false, true, false, ""},
		{"ipv6 no port", "[::1]", "[::1]:1234", "", false, true, false, ""},
		{"bare ipv6", "::1", "[::1]:1234", "", false, true, false, ""},
		{"loopback host from a remote peer", "localhost", "203.0.113.9:1234", "", false, true, false, ""},
		{"spoof local scheme", "localhost:8080", "127.0.0.1:1234", "https", true, true, false, ""},
		{"listed name, loopback v4 peer, one https", proxied, "127.0.0.1:1234", "https", true, true, true, ""},
		{"listed name, loopback v6 peer", proxied, "[::1]:1234", "https", true, true, true, ""},
		{"listed name mixed case", "App.Example.Test", "127.0.0.1:1234", "https", true, true, true, ""},
		{"listed with its own port", proxied + ":8443", "127.0.0.1:1234", "https", true, false, false, codeInvalidHost}, // bare entry does not admit a port
		{"not listed", proxied, "127.0.0.1:1234", "https", false, false, false, codeInvalidHost},
		{"missing proto", proxied, "127.0.0.1:1234", "", true, false, false, codeUntrustedProxy},
		{"plaintext proto", proxied, "127.0.0.1:1234", "http", true, false, false, codeUntrustedProxy},
		{"ambiguous proto in one value", proxied, "127.0.0.1:1234", "https,http", true, false, false, codeUntrustedProxy},
		{"uppercase proto", proxied, "127.0.0.1:1234", "HTTPS", true, false, false, codeUntrustedProxy},
		{"remote peer", proxied, "203.0.113.9:1234", "https", true, false, false, codeUntrustedProxy},
		{"unparsable peer", proxied, "localhost:1234", "https", true, false, false, codeUntrustedProxy},
		{"peer without port", proxied, "127.0.0.1", "https", true, false, false, codeUntrustedProxy},
		{"alias not listed", "app.alias.example.test", "127.0.0.1:1234", "https", true, false, false, codeInvalidHost},
		{"foreign", "evil.example", "127.0.0.1:1234", "https", true, false, false, codeInvalidHost},
		{"rebinding lookalike", "localhost.evil.example", "127.0.0.1:1234", "", true, false, false, codeInvalidHost},
		{"private address", "192.0.2.5:8085", "127.0.0.1:1234", "", true, false, false, codeInvalidHost},
		{"empty host", "", "127.0.0.1:1234", "", true, false, false, codeInvalidHost},
		{"alternate port on listed name", proxied + ":443", "127.0.0.1:1234", "https", true, false, false, codeInvalidHost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/account", nil)
			r.Host = tc.host
			r.RemoteAddr = tc.peer
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			r.Header.Set("X-Forwarded-Host", proxied)
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			r.Header.Set("X-Real-IP", "203.0.113.9")
			r.Header.Set("Forwarded", "host="+proxied+";proto=https")
			var allowed []string
			if tc.listed {
				allowed = []string{proxied}
			}
			called := false
			h := hostGuard(allowed, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				for _, name := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "X-Real-Ip"} {
					if r.Header.Get(name) != "" {
						t.Fatalf("%s leaked downstream", name)
					}
				}
				if got := r.Header.Get("X-Forwarded-Proto") != ""; got != tc.keepsProto {
					t.Fatalf("X-Forwarded-Proto kept = %v, want %v", got, tc.keepsProto)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if called != tc.accepted {
				t.Fatalf("handler reached = %v, want %v (status %d)", called, tc.accepted, w.Code)
			}
			if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("response hardening headers missing")
			}
			if tc.accepted {
				return
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403", w.Code)
			}
			var b struct{ Code, Message string }
			if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
				t.Fatalf("body is not the flat JSON shape: %q", w.Body)
			}
			if b.Code != tc.wantCode || b.Message == "" {
				t.Fatalf("body = %+v, want code %s with a message", b, tc.wantCode)
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("content type %q", w.Header().Get("Content-Type"))
			}
			// The refusal never echoes what the client sent.
			if tc.host != "" && strings.Contains(w.Body.String(), strings.ToLower(tc.host)) {
				t.Fatalf("body echoes the Host: %s", w.Body)
			}
		})
	}
}

func TestTwoXForwardedProtoLinesAreAmbiguous(t *testing.T) {
	h := hostGuard([]string{proxied}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler reached") }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = proxied, "127.0.0.1:1"
	r.Header.Add("X-Forwarded-Proto", "https")
	r.Header.Add("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("two https lines: %d, want 403", w.Code)
	}
}

func TestTheOriginalRequestIsNotMutated(t *testing.T) {
	// The guard clones before deleting, so a wrapper above it still sees what arrived.
	var inner *http.Request
	h := hostGuard(nil, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { inner = r }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = "localhost", "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if inner == nil || inner.Header.Get("X-Forwarded-For") != "" {
		t.Fatal("not stripped downstream")
	}
	if r.Header.Get("X-Forwarded-For") == "" {
		t.Fatal("the original request was mutated")
	}
}

func TestClientAddressIsRecordedOnlyForAValidatedProxyRequest(t *testing.T) {
	for _, tc := range []struct {
		name, host, peer string
		proto            string
		xff              []string
		want             string
	}{
		{"validated, one hop", proxied, "127.0.0.1:9", "https", []string{"203.0.113.5"}, "203.0.113.5"},
		{"validated, last hop of a chain", proxied, "127.0.0.1:9", "https", []string{"1.2.3.4, 198.51.100.7, 203.0.113.5"}, "203.0.113.5"},
		{"validated, last line", proxied, "127.0.0.1:9", "https", []string{"198.51.100.7", "203.0.113.5"}, "203.0.113.5"},
		{"validated, ipv6", proxied, "127.0.0.1:9", "https", []string{"2001:db8::5"}, "2001:db8::5"},
		{"validated, spaces", proxied, "127.0.0.1:9", "https", []string{"1.2.3.4 ,  203.0.113.5 "}, "203.0.113.5"},
		{"validated, no header", proxied, "127.0.0.1:9", "https", nil, "127.0.0.1"},
		{"validated, malformed", proxied, "127.0.0.1:9", "https", []string{"nope"}, "127.0.0.1"},
		{"validated, with port", proxied, "127.0.0.1:9", "https", []string{"203.0.113.5:80"}, "127.0.0.1"},
		{"validated, trailing comma", proxied, "127.0.0.1:9", "https", []string{"203.0.113.5,"}, "127.0.0.1"},
		{"validated, bracketed", proxied, "127.0.0.1:9", "https", []string{"[2001:db8::5]"}, "127.0.0.1"},
		{"validated, zone", proxied, "127.0.0.1:9", "https", []string{"fe80::1%en0"}, "127.0.0.1"},
		{"loopback host ignores the header", "localhost", "127.0.0.1:9", "https", []string{"203.0.113.5"}, "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host, r.RemoteAddr = tc.host, tc.peer
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			var got string
			hostGuard([]string{proxied}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = clientaddr.From(r)
			})).ServeHTTP(httptest.NewRecorder(), r)
			if got != tc.want {
				t.Fatalf("client address = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseAllowedHosts(t *testing.T) {
	good := map[string]any{"allowed_hosts": []any{"App.Example.Test", "b.example.test:8443", "[::1]"}}
	got, err := parseAllowedHosts(good)
	if err != nil || strings.Join(got, ",") != "app.example.test,b.example.test:8443,[::1]" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := parseAllowedHosts(nil); err != nil || len(got) != 0 {
		t.Fatalf("absent key: %v, %v", got, err)
	}
	if got, err := parseAllowedHosts(map[string]any{"allowed_hosts": []any{}}); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v, %v", got, err)
	}
	for _, bad := range []any{
		[]any{""}, []any{"*"}, []any{"*.example.test"}, []any{"https://app.example.test"},
		[]any{"app.example.test/"}, []any{"user@app.example.test"}, []any{" app.example.test"},
		[]any{"a b"}, []any{7}, "app.example.test", 5, []any{"ok.example.test", ""},
	} {
		if _, err := parseAllowedHosts(map[string]any{"allowed_hosts": bad}); err == nil {
			t.Errorf("%v: want an error", bad)
		} else if !strings.Contains(err.Error(), "httphost.allowed_hosts") {
			t.Errorf("%v: the error should name the key: %v", bad, err)
		}
	}
}

func TestLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "[::1]:80": true, "localhost:80": true,
		"0.0.0.0:80": false, ":80": false, "198.51.100.1:80": false, "[::]:80": false, "garbage": false,
	} {
		if got := loopbackAddr(addr); got != want {
			t.Errorf("loopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
