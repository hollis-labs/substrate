package forwarded_test

import (
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/substrate/forwarded"
)

func TestHTTPS(t *testing.T) {
	for _, c := range []struct {
		name string
		peer string
		xfp  []string
		want bool
	}{
		{"loopback, one https", "127.0.0.1:5000", []string{"https"}, true},
		{"ipv6 loopback", "[::1]:5000", []string{"https"}, true},
		{"spaces and case", "127.0.0.1:5000", []string{" HTTPS "}, true},
		{"not a loopback peer", "192.0.2.10:5555", []string{"https"}, false},
		{"two values", "127.0.0.1:5000", []string{"https", "https"}, false},
		{"http", "127.0.0.1:5000", []string{"http"}, false},
		{"none", "127.0.0.1:5000", nil, false},
		{"unparsable peer", "@", []string{"https"}, false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		for _, v := range c.xfp {
			r.Header.Add("X-Forwarded-Proto", v)
		}
		if got := forwarded.HTTPS(r); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
