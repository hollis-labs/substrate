package connectmatrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Minted is a token the issuer produced. Token is never logged or reported.
type Minted struct {
	Token string
	ID    string // what Revoke needs (the jti)
}

// MintRequest describes the token wanted.
type MintRequest struct {
	Name     string   `json:"name"`
	App      string   `json:"app,omitempty"` // caller identity; Name when empty
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes"`
	Kind     string   `json:"kind"`
}

// Issuer mints and revokes service tokens.
type Issuer interface {
	Mint(ctx context.Context, r MintRequest) (Minted, error)
	Revoke(ctx context.Context, m Minted) error
}

// NewIssuer builds the issuer a config names.
func NewIssuer(c IssuerConfig, client *http.Client) (Issuer, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	switch c.Type {
	case "mock":
		return &httpIssuer{client: client, mint: strings.TrimRight(c.URL, "/") + "/mock/mint", revoke: strings.TrimRight(c.URL, "/") + "/mock/revoke"}, nil
	case "cli":
		return &cliIssuer{cmd: c.Command, global: c.GlobalArgs}, nil
	case "http":
		return &httpIssuer{client: client, mint: c.MintURL, revoke: c.RevokeURL, credFile: c.CredentialFile}, nil
	}
	return nil, fmt.Errorf("unknown issuer type %q", c.Type)
}

type httpIssuer struct {
	client       *http.Client
	mint, revoke string
	credFile     string // optional bearer for the issuer's own API
}

func (h *httpIssuer) post(ctx context.Context, target string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid issuer URL") // the URL may hold a secret: never echo it
	}
	req.Header.Set("Content-Type", "application/json")
	if h.credFile != "" {
		b, err := os.ReadFile(h.credFile) // #nosec G304 -- operator-supplied path
		if err != nil {
			return fmt.Errorf("issuer credential: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
	}
	resp, err := sameOrigin(h.client, req.URL).Do(req) // #nosec G704 -- operator-supplied URL
	if err != nil {
		return errors.New("issuer request failed: " + scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("issuer answered %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (h *httpIssuer) Mint(ctx context.Context, r MintRequest) (Minted, error) {
	r.Kind = "service"
	var out struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := h.post(ctx, h.mint, r, &out); err != nil {
		return Minted{}, err
	}
	if out.Token == "" || out.ID == "" {
		return Minted{}, errors.New("issuer reply lacks token or id")
	}
	return Minted{Token: out.Token, ID: out.ID}, nil
}

func (h *httpIssuer) Revoke(ctx context.Context, m Minted) error {
	return h.post(ctx, h.revoke, map[string]string{"id": m.ID}, nil)
}

// scrub keeps a transport error useful without echoing a URL that may carry
// a secret in its query.
func scrub(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return "request failed"
}

// sameOrigin returns a copy of c that refuses any redirect leaving the origin
// (scheme, host and port) of the first request, so the issuer credential is
// never forwarded to another service, including another port on the same host.
func sameOrigin(c *http.Client, first *url.URL) *http.Client {
	cc := *c
	origin := first.Scheme + "://" + first.Host
	cc.CheckRedirect = func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme+"://"+r.URL.Host != origin {
			return errors.New("refusing cross-origin redirect")
		}
		return nil
	}
	return &cc
}

// cliIssuer drives the Tether operator CLI. Exec without a shell; the token is
// read from stdout and never appears in an error.
type cliIssuer struct {
	cmd    string
	global []string
}

func (c *cliIssuer) Mint(ctx context.Context, r MintRequest) (Minted, error) {
	app := r.App
	if app == "" {
		app = r.Name
	}
	args := append([]string{"service-token", "issue"}, c.global...)
	args = append(args, "--app", app, "--audience", r.Audience, "--label", "connect-matrix", "--json")
	for _, s := range r.Scopes {
		args = append(args, "--scope", s)
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, c.cmd, args...) // #nosec G204 -- operator-configured command, no shell
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return Minted{}, errors.New("issuer command failed: " + firstLine(errb.String()))
	}
	var d struct {
		Token string `json:"token"`
		JTI   string `json:"jti"`
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.Token == "" || d.JTI == "" {
		return Minted{}, errors.New("issuer command reply lacks token or jti")
	}
	return Minted{Token: d.Token, ID: d.JTI}, nil
}

func (c *cliIssuer) Revoke(ctx context.Context, m Minted) error {
	args := append([]string{"service-token", "revoke", m.ID}, c.global...)
	var errb bytes.Buffer
	cmd := exec.CommandContext(ctx, c.cmd, args...) // #nosec G204
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return errors.New("issuer revoke failed: " + firstLine(errb.String()))
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "no output"
	}
	return s
}
