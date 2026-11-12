package httphost

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/clientaddr"
)

const direct = "tail.example.test"

// A direct host is served to any peer with nothing believed: every forwarding header is
// stripped (the proto too), the client address is the peer, and the other lists are unchanged.
func TestDirectHosts(t *testing.T) {
	for _, tc := range []struct {
		name, host, peer, proto string
		accepted                bool
		wantCode                string
	}{
		{"remote peer", direct, "198.51.100.7:5000", "", true, ""},
		{"loopback peer", direct, "127.0.0.1:5000", "", true, ""},
		{"ipv6 remote peer", direct, "[2001:db8::7]:5000", "", true, ""},
		{"mixed case", "Tail.Example.Test", "198.51.100.7:5000", "", true, ""},
		{"remote peer claiming https", direct, "198.51.100.7:5000", "https", true, ""},
		{"loopback peer claiming https", direct, "127.0.0.1:5000", "https", true, ""},
		{"own port is not the bare entry", direct + ":8443", "198.51.100.7:5000", "", false, codeInvalidHost},
		{"foreign host on the same bind", "evil.example", "198.51.100.7:5000", "", false, codeInvalidHost},
		{"lookalike suffix", direct + ".evil.example", "198.51.100.7:5000", "", false, codeInvalidHost},
		{"lookalike prefix", "x" + direct, "198.51.100.7:5000", "", false, codeInvalidHost},
		{"proxied name still needs the proxy", proxied, "198.51.100.7:5000", "https", false, codeUntrustedProxy},
		{"empty host", "", "198.51.100.7:5000", "", false, codeInvalidHost},
		{"loopback name from a remote peer", "localhost", "198.51.100.7:5000", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/account", nil)
			r.Host, r.RemoteAddr = tc.host, tc.peer
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			r.Header.Set("X-Real-IP", "203.0.113.9")
			r.Header.Set("Forwarded", "for=203.0.113.9;proto=https")
			wantPeer, _, _ := net.SplitHostPort(tc.peer)
			called := false
			h := hostGuardDirect([]string{proxied}, []string{direct}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				for _, name := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "X-Real-Ip", "X-Forwarded-Proto"} {
					if r.Header.Get(name) != "" {
						t.Fatalf("%s leaked downstream", name)
					}
				}
				if got := clientaddr.From(r); got != wantPeer {
					t.Fatalf("client address = %q, want the peer %q", got, wantPeer)
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
			if !tc.accepted {
				var b struct{ Code string }
				if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || w.Code != http.StatusForbidden || b.Code != tc.wantCode {
					t.Fatalf("refusal = %d %q, want 403 %s", w.Code, w.Body, tc.wantCode)
				}
			}
		})
	}
}

func TestADirectHostIsNotServedWithoutTheList(t *testing.T) {
	h := hostGuardDirect(nil, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler reached") }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = direct, "198.51.100.7:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("empty direct list: %d, want 403", w.Code)
	}
}

func TestParseHosts(t *testing.T) {
	allowed, dir, err := parseHosts(map[string]any{
		"allowed_hosts": []any{"A.example.test"}, "direct_hosts": []any{"Tail.Example.Test", "[::1]:9"},
	})
	if err != nil || strings.Join(allowed, ",") != "a.example.test" || strings.Join(dir, ",") != "tail.example.test,[::1]:9" {
		t.Fatalf("got %v %v %v", allowed, dir, err)
	}
	if a, d, err := parseHosts(nil); err != nil || len(a) != 0 || len(d) != 0 {
		t.Fatalf("absent keys: %v %v %v", a, d, err)
	}
	for _, bad := range []any{
		[]any{""}, []any{"*"}, []any{"*.example.test"}, []any{"https://t.example.test"},
		[]any{"t.example.test/"}, []any{"user@t.example.test"}, []any{" t.example.test"},
		[]any{"a b"}, []any{7}, "t.example.test", 5, []any{"ok.example.test", ""},
	} {
		if _, _, err := parseHosts(map[string]any{"direct_hosts": bad}); err == nil {
			t.Errorf("%v: want an error", bad)
		} else if !strings.Contains(err.Error(), "httphost.direct_hosts") {
			t.Errorf("%v: the error should name the key: %v", bad, err)
		}
	}
	// A name in both lists is refused whatever the case, and the error names both keys.
	_, _, err = parseHosts(map[string]any{"allowed_hosts": []any{"x.example.test"}, "direct_hosts": []any{"X.Example.Test"}})
	if err == nil || !strings.Contains(err.Error(), "direct_hosts") || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Fatalf("overlap: %v", err)
	}
	// The same name with a port is a different Host, so it does not overlap.
	if _, _, err := parseHosts(map[string]any{"allowed_hosts": []any{"x.example.test"}, "direct_hosts": []any{"x.example.test:8443"}}); err != nil {
		t.Fatalf("different port: %v", err)
	}
}
