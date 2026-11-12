package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// token is the claim set of a minted JWT; the names are the ones
// libs/security/auth's verifier reads, so any app can verify it from the JWKS.
type claims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Scope string `json:"scope"`
	Exp   int64  `json:"exp"`
	Nbf   int64  `json:"nbf"`
	Iat   int64  `json:"iat"`
	JTI   string `json:"jti"`
	Kind  string `json:"kind,omitempty"`
	SID   string `json:"sid,omitempty"`
	Par   string `json:"par,omitempty"`
}

// mint signs a token.
func (s *Service) mint(c claims) (string, error) {
	st := s.keys.Load()
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": st.kid})
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(body)
	sig := ed25519.Sign(st.priv, []byte(signing))
	return signing + "." + enc.EncodeToString(sig), nil
}

// newClaims fills the fixed parts of a token for sub.
func (s *Service) newClaims(sub, kind, audience string, scopes []string, ttl time.Duration, now time.Time) claims {
	return claims{
		Iss: s.cfg.issuer, Sub: sub, Aud: audience, Scope: strings.Join(scopes, " "),
		Exp: now.Add(ttl).Unix(), Nbf: now.Add(-5 * time.Second).Unix(), Iat: now.Unix(),
		JTI: newID("tok_"), Kind: kind,
	}
}

// grantable returns the scopes a caller may hand out: requested when given, and
// always within what the caller itself holds ("*" holds anything).
func grantable(held, requested []string) ([]string, bool) {
	if len(requested) == 0 {
		return held, true
	}
	out := make([]string, 0, len(requested))
	for _, r := range requested {
		if !covers(held, r) {
			return nil, false
		}
		out = append(out, r)
	}
	return out, true
}

// covers reports whether the scopes held include want, with the same wildcard
// rules as a principal: exact, "*", or "<app>.*".
func covers(held []string, want string) bool {
	for _, g := range held {
		if g == want || g == "*" {
			return true
		}
		if pre, ok := strings.CutSuffix(g, ".*"); ok && strings.HasPrefix(want, pre+".") {
			return true
		}
	}
	return false
}
