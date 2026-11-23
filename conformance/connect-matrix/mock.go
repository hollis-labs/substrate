package connectmatrix

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ServiceTokenLifetime is the exp the mock gives service tokens: go-auth
// rejects a token without exp, so "no expiry" is a distant one.
const ServiceTokenLifetime = 10 * 365 * 24 * time.Hour

// MockIssuer stands in for Tether: it publishes a JWKS and a deny-list and
// mints and revokes service tokens, so a target configured to trust it can be
// proven with no real issuer. State is in memory.
type MockIssuer struct {
	// Name is the "iss" claim; targets must be configured with the same value.
	Name string

	priv ed25519.PrivateKey
	kid  string

	mu     sync.Mutex
	n      int
	denied []string
}

// NewMockIssuer creates an issuer with a fresh signing key.
func NewMockIssuer(name string) (*MockIssuer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sum := base64.RawURLEncoding.EncodeToString(pub[:6])
	return &MockIssuer{Name: name, priv: priv, kid: "mock-" + sum}, nil
}

// PublicKeyPEM is the PKIX PEM public key, usable as a bootstrap key file.
func (m *MockIssuer) PublicKeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(m.priv.Public())
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// Handler serves /.well-known/jwks.json, /auth/denylist, /mock/mint and
// /mock/revoke.
func (m *MockIssuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "use": "sig", "kid": m.kid,
			"x": base64.RawURLEncoding.EncodeToString(m.priv.Public().(ed25519.PublicKey)),
		}}})
	})
	mux.HandleFunc("GET /auth/denylist", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		doc := map[string][]string{"jti": append([]string{}, m.denied...), "sub": {}, "sid": {}}
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("POST /mock/mint", func(w http.ResponseWriter, r *http.Request) {
		var req MintRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Audience == "" || len(req.Scopes) == 0 {
			http.Error(w, "audience and scopes are required", http.StatusBadRequest)
			return
		}
		tok, jti := m.mint(req)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok, "id": jti})
	})
	mux.HandleFunc("POST /mock/revoke", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.ID == "" {
			http.Error(w, "id is required", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.denied = append(m.denied, req.ID)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (m *MockIssuer) mint(r MintRequest) (token, jti string) {
	m.mu.Lock()
	m.n++
	jti = "mock-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(m.n)
	m.mu.Unlock()
	now := time.Now()
	scope := ""
	for i, s := range r.Scopes {
		if i > 0 {
			scope += " "
		}
		scope += s
	}
	claims := map[string]any{
		"iss": m.Name, "sub": "msg://service/mock/" + r.Name, "aud": r.Audience, "scope": scope,
		"kind": "service", "jti": jti, "iat": now.Unix(), "exp": now.Add(ServiceTokenLifetime).Unix(),
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	in := enc(map[string]string{"alg": "EdDSA", "kid": m.kid, "typ": "JWT"}) + "." + enc(claims)
	return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(m.priv, []byte(in))), jti
}
