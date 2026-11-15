package identity_test

import (
	"net/http"
	"testing"
)

func peer(addr string) opt { return func(r *http.Request) { r.RemoteAddr = addr } }

// identity.trusted_proxy believes X-Forwarded-Proto only as package forwarded does (one https
// value from a loopback peer), the rule httphost.Harden shares: the session cookie is Secure
// through a local TLS proxy and not when a remote peer claims https.
func TestTrustedProxyIsTheSharedLoopbackRule(t *testing.T) {
	for _, c := range []struct {
		name   string
		peer   string
		secure bool
	}{
		{"local proxy", "127.0.0.1:4000", true},
		{"remote peer claiming https", "192.0.2.10:1234", false},
	} {
		a := start(t, "enforce", `, "trusted_proxy": true`)
		w := a.do("POST", "/api/setup", map[string]string{"display_name": "Owner", "username": "owner", "password": password},
			peer(c.peer), header("X-Forwarded-Proto", "https"))
		code(t, w, 200)
		ck := w.cookie("demo_session")
		if ck == nil {
			t.Fatalf("%s: no session cookie", c.name)
		}
		if ck.Secure != c.secure {
			t.Errorf("%s: Secure %v, want %v", c.name, ck.Secure, c.secure)
		}
	}
}
