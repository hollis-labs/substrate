package auth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
)

// KeySource returns the app's current verification keys as a JWKS document
// ({"keys":[{"kty":"OKP","crv":"Ed25519","kid":..,"x":..}]}): every key a live token
// may be signed with, so the active key and any still in their rotation overlap. It is
// called in process, never over HTTP. It must be cheap and safe for concurrent use.
type KeySource func(ctx context.Context) ([]byte, error)

var (
	hookMu    sync.Mutex
	keySource KeySource
	denyList  *libauth.DenyList
)

// UseKeySource makes the JWT verifier read its keys from src, in process, for an app
// that is its own issuer with a rotating key ring (it cannot fetch jwks_url from itself:
// its listener is not up when this module starts). Call it before boot.Start; nil
// removes it. It is a second trust root beside the identity module's (a token either
// verifies is accepted); the config's issuer and audience (default the app name) apply.
// Start refuses it together with jwks_url.
//
// Keys are read on first use, every 30 seconds, on a token whose kid is unknown (at
// most once a second), and when the app calls RefreshKeys. Call RefreshKeys after
// adding or retiring a key so a retired key stops verifying at once.
func UseKeySource(src KeySource) {
	hookMu.Lock()
	defer hookMu.Unlock()
	keySource = src
}

// UseDenyList makes the JWT verifier refuse tokens d lists (jti, sub or sid), for an app
// that keeps its deny-list in process; the app updates d (Replace) and the next
// verification sees it. Call it before boot.Start; nil removes it. It needs the JWT
// verifier (UseKeySource, jwks_url or bootstrap_key_file); Start refuses it together
// with denylist_url.
func UseDenyList(d *libauth.DenyList) {
	hookMu.Lock()
	defer hookMu.Unlock()
	denyList = d
}

func hooks() (KeySource, *libauth.DenyList) {
	hookMu.Lock()
	defer hookMu.Unlock()
	return keySource, denyList
}

// KeysKey holds the *libauth.JWTVerifier the module built from config or UseKeySource.
const KeysKey = "auth.jwt"

// RefreshKeys re-reads the JWT verifier's keys now (from the KeySource or jwks_url). An
// app that rotates its own keys calls it after adding or retiring one. It does nothing
// when no JWT verifier is configured.
func RefreshKeys(ctx context.Context, app *module.App) error {
	v, _ := app.Value(KeysKey)
	jv, ok := v.(*libauth.JWTVerifier)
	if !ok {
		return nil
	}
	return jv.Refresh(ctx)
}

// keySourceURL is the JWKS URL the verifier is given when the keys come from a
// KeySource. Its client's transport answers it in process; the .invalid name (RFC 6761)
// never resolves, so nothing can reach the network even by mistake.
const keySourceURL = "http://key-source.invalid/jwks.json"

// sourceTransport answers the verifier's JWKS fetch from a KeySource. libs JWTVerifier
// fetches over an *http.Client; serving that one request in process reuses its key
// cache, rotation and checks as they are instead of copying them.
type sourceTransport struct{ src KeySource }

func (t sourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != keySourceURL {
		return nil, fmt.Errorf("auth: key source: unexpected request for %s", r.URL.Redacted())
	}
	b, err := t.src(r.Context())
	if err != nil {
		return nil, fmt.Errorf("auth: key source: %w", err)
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(b)),
		ContentLength: int64(len(b)),
		Request:       r,
	}, nil
}

func keySourceConfig(src KeySource) libauth.JWTConfig {
	return libauth.JWTConfig{
		JWKSURL:         keySourceURL,
		Client:          &http.Client{Transport: sourceTransport{src}},
		RefreshInterval: 30 * time.Second,
		MinRefresh:      time.Second,
	}
}
