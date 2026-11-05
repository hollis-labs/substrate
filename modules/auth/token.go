package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
)

// TokenSource returns the bearer token to present to another service.
type TokenSource func(ctx context.Context) (string, error)

// tokenSource builds the outbound source from config: a static service token
// file, or a refresher over the issuer's mint endpoint. Neither means nil.
func tokenSource(ctx context.Context, cfg map[string]any) (TokenSource, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return s }
	file, mint := str("token_file"), str("mint_url")
	switch {
	case file != "" && mint != "":
		return nil, errors.New("auth: set token_file or mint_url, not both")
	case file != "":
		return func(context.Context) (string, error) { return readToken(file) }, nil
	case mint == "":
		return nil, nil
	}
	cred := str("mint_credential_file")
	if cred == "" {
		return nil, errors.New("auth: mint_url needs mint_credential_file")
	}
	body, _ := cfg["mint_body"].(map[string]any)
	if body == nil {
		body = map[string]any{}
	}
	m := &libauth.HTTPMinter{URL: mint, Body: body, Credential: func() (string, error) { return readToken(cred) }}
	r, err := libauth.NewRefresher(libauth.RefresherConfig{Mint: m.Mint})
	if err != nil {
		return nil, err
	}
	go r.Run(ctx)
	return r.Token, nil
}

// readToken reads a token file. The value is never put in an error.
func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("auth: cannot read the token file")
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", errors.New("auth: token file is empty")
	}
	return t, nil
}

// Token returns the outbound token source the auth module built from config, or
// nil when none is configured (the dev default).
func Token(app *module.App) TokenSource {
	v, _ := app.Value(TokenKey)
	ts, _ := v.(TokenSource)
	return ts
}

// Transport wraps base so requests carry the configured token as a bearer
// credential. A request that already has an Authorization header is left alone.
// When hosts are given the token is attached only to those hosts, so it cannot
// leak to a redirect or an unrelated URL; with none given it goes to every host,
// which is for clients whose base URL is fixed. With no token configured it
// attaches nothing.
func Transport(app *module.App, base http.RoundTripper, hosts ...string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	ts := Token(app)
	if ts == nil {
		return base
	}
	allowed := map[string]bool{}
	for _, h := range hosts {
		allowed[h] = true
	}
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || (len(allowed) > 0 && !allowed[r.URL.Host]) {
			return base.RoundTrip(r)
		}
		tok, err := ts(r.Context())
		if err != nil {
			return nil, err
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+tok)
		return base.RoundTrip(r)
	})
}

// HTTPClient is an http.Client whose requests carry the configured token (see Transport).
func HTTPClient(app *module.App, hosts ...string) *http.Client {
	return &http.Client{Transport: Transport(app, nil, hosts...)}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
