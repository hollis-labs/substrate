// Package connectmatrix proves app-to-app API connectivity: for each pair it
// obtains a service token from an issuer, calls the target with it, checks the
// target refuses a token meant for someone else, revokes the token and checks
// the target stops accepting it within the check's wait. It never prints a token.
package connectmatrix

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the JSON file `connect-matrix run` reads.
type Config struct {
	Issuer IssuerConfig `json:"issuer"`
	// RevokeBoundSeconds is how long the revoked check waits for the target to
	// start refusing the token. Default 45 (one 30s deny-list poll interval
	// plus slack). It is the check's patience, not a property of the target: a
	// target whose deny-list poll fails keeps the last list (stale) and will
	// fail this check.
	RevokeBoundSeconds int `json:"revoke_bound_seconds"`
	// PollIntervalMillis is how often a revoked token is retried. Default 500.
	PollIntervalMillis int    `json:"poll_interval_ms"`
	Pairs              []Pair `json:"pairs"`
}

// IssuerConfig selects where tokens come from.
//
// type "mock": a `connect-matrix mock-issuer` instance at URL.
//
// type "http": a real issuer. MintURL receives POST {"name","audience",
// "scopes","kind":"service"} with the bearer read from CredentialFile and
// answers {"token","id"}; RevokeURL receives POST {"id"} with the same bearer.
// These shapes are a placeholder until the Tether service-token issuing
// command lands (CW-20261019-0044); adjust the issuer then, not the pairs.
type IssuerConfig struct {
	Type           string `json:"type"`
	URL            string `json:"url,omitempty"`
	MintURL        string `json:"mint_url,omitempty"`
	RevokeURL      string `json:"revoke_url,omitempty"`
	CredentialFile string `json:"credential_file,omitempty"`
}

// Pair is one app-to-app connection to prove.
type Pair struct {
	Name     string   `json:"name"`
	Target   string   `json:"target"` // base URL of the app being called
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes"`
	Probe    Probe    `json:"probe"`
	// OtherAudience is the audience of the token used for the wrong-audience
	// check; default "connect-matrix-other".
	OtherAudience string `json:"other_audience,omitempty"`
	// Enforcing says the target runs with its auth dial on enforce (or a
	// legacy gate that rejects unknown bearers). When false the wrong-audience
	// and revocation checks are skipped, because a target that admits
	// anonymous callers cannot show a rejection.
	Enforcing *bool `json:"enforcing,omitempty"`
}

// Probe is the request made with the token.
type Probe struct {
	Method   string `json:"method"`    // default GET
	Path     string `json:"path"`      // required
	OKStatus int    `json:"ok_status"` // default 200
	Body     string `json:"body,omitempty"`
}

// LoadConfig reads and validates a config file.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, c.Validate()
}

// Validate fills defaults and rejects an unusable config.
func (c *Config) Validate() error {
	switch c.Issuer.Type {
	case "mock":
		if c.Issuer.URL == "" {
			return errors.New("config: issuer.url is required for type mock")
		}
		if err := plainURL("issuer.url", c.Issuer.URL); err != nil {
			return err
		}
	case "http":
		if c.Issuer.MintURL == "" || c.Issuer.RevokeURL == "" || c.Issuer.CredentialFile == "" {
			return errors.New("config: issuer.mint_url, revoke_url and credential_file are required for type http")
		}
		if err := plainURL("issuer.mint_url", c.Issuer.MintURL); err != nil {
			return err
		}
		if err := plainURL("issuer.revoke_url", c.Issuer.RevokeURL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("config: issuer.type %q (want mock or http)", c.Issuer.Type)
	}
	if c.RevokeBoundSeconds <= 0 {
		c.RevokeBoundSeconds = 45
	}
	if c.PollIntervalMillis <= 0 {
		c.PollIntervalMillis = 500
	}
	if len(c.Pairs) == 0 {
		return errors.New("config: no pairs")
	}
	seen := map[string]bool{}
	for i := range c.Pairs {
		p := &c.Pairs[i]
		if p.Name == "" || p.Target == "" || p.Audience == "" || p.Probe.Path == "" {
			return fmt.Errorf("config: pair %d needs name, target, audience and probe.path", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: duplicate pair %q", p.Name)
		}
		seen[p.Name] = true
		if err := plainURL(fmt.Sprintf("pair %q target", p.Name), p.Target); err != nil {
			return err
		}
		if len(p.Scopes) == 0 {
			return fmt.Errorf("config: pair %q needs at least one scope", p.Name)
		}
		if p.Probe.Method == "" {
			p.Probe.Method = "GET"
		}
		if p.Probe.OKStatus == 0 {
			p.Probe.OKStatus = 200
		}
		// "accepted" must show the target ADMITTED the token. A 401 or 403 is a
		// refusal, and a target that answers 401 to everything would otherwise
		// satisfy every check.
		if p.Probe.OKStatus < 200 || p.Probe.OKStatus > 299 {
			return fmt.Errorf("config: pair %q probe.ok_status must be a 2xx status (got %d): a refusal is not proof of connectivity", p.Name, p.Probe.OKStatus)
		}
		if p.OtherAudience == "" {
			p.OtherAudience = "connect-matrix-other"
		}
		if p.OtherAudience == p.Audience {
			return fmt.Errorf("config: pair %q other_audience equals audience", p.Name)
		}
	}
	return nil
}

// Bound is RevokeBoundSeconds as a duration.
func (c Config) Bound() time.Duration { return time.Duration(c.RevokeBoundSeconds) * time.Second }

// Poll is PollIntervalMillis as a duration.
func (c Config) Poll() time.Duration { return time.Duration(c.PollIntervalMillis) * time.Millisecond }

func (p Pair) enforcing() bool { return p.Enforcing == nil || *p.Enforcing }

// plainURL requires an absolute http(s) URL with no userinfo, query or
// fragment, so a secret cannot ride in a configured URL into a report or an
// error. The message names the field, never the value.
func plainURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: %s must be an absolute http(s) URL", field)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("config: %s must not contain credentials, a query or a fragment; pass secrets by file or header, not in the URL", field)
	}
	return nil
}
