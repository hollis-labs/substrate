package connectmatrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Check outcomes.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
)

// Pair statuses, the cells of the matrix.
const (
	StatusPass        = "pass"
	StatusFail        = "fail"
	StatusNotEnforced = "not-enforced"
	StatusNA          = "n-a"
)

// CheckResult is one step of a pair's proof.
type CheckResult struct {
	Name    string  `json:"name"`
	Result  string  `json:"result"`
	Detail  string  `json:"detail,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
}

// PairResult is the outcome for one pair.
type PairResult struct {
	Name   string        `json:"name"`
	Caller string        `json:"caller"`
	App    string        `json:"app"` // target app name (pair name after "->")
	Target string        `json:"target"`
	Status string        `json:"status"` // pass, fail, not-enforced or n-a
	Pass   bool          `json:"pass"`   // false only for fail
	Checks []CheckResult `json:"checks"`
}

// Runner proves pairs against running targets.
type Runner struct {
	Issuer Issuer
	Client *http.Client
	Cfg    Config
	// Now and Sleep are replaceable for tests. Sleep must return early when ctx
	// is done.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration)
}

// Run proves each pair (or only those named) and returns the results in
// config order.
func (r *Runner) Run(ctx context.Context, only map[string]bool) []PairResult {
	if r.Client == nil {
		r.Client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Sleep == nil {
		r.Sleep = sleepCtx
	}
	var out []PairResult
	for _, p := range r.Cfg.Pairs {
		if len(only) > 0 && !only[p.Name] {
			continue
		}
		out = append(out, r.runPair(ctx, p))
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// SafeTarget is the target as it may appear in a report: scheme and host only.
// Userinfo, path, query and fragment can carry secrets and are dropped; a value
// that does not parse is replaced, never echoed.
func SafeTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "<invalid target>"
	}
	return u.Scheme + "://" + u.Host
}

func (r *Runner) runPair(ctx context.Context, p Pair) (res PairResult) {
	res = PairResult{Name: p.Name, Caller: p.Caller, App: p.Name, Target: SafeTarget(p.Target), Pass: true}
	if i := strings.Index(p.Name, "->"); i >= 0 {
		res.App = p.Name[i+2:]
	}
	defer func() {
		switch {
		case !res.Pass:
			res.Status = StatusFail
		case p.NA != "":
			res.Status = StatusNA
		case res.Status == "":
			res.Status = StatusPass
		}
	}()
	if p.NA != "" {
		res.Checks = append(res.Checks, CheckResult{Name: "applicable", Result: Skip, Detail: p.NA})
		return res
	}
	add := func(c CheckResult) bool {
		res.Checks = append(res.Checks, c)
		if c.Result == Fail {
			res.Pass = false
		}
		return c.Result != Fail
	}

	good, err := r.Issuer.Mint(ctx, MintRequest{Name: p.Name, App: p.Caller, Audience: p.Audience, Scopes: p.Scopes})
	if !add(check("mint", err, "issuer minted a service token")) {
		return res
	}
	// Whatever happens next, the token is revoked on the way out and the
	// outcome is recorded: a long-lived token must never be left behind
	// silently. The enforcing path revokes it deliberately (and records
	// "revoke"); this only covers every other exit.
	goodRevoked := false
	defer func() {
		if !goodRevoked {
			add(check("cleanup", r.Issuer.Revoke(ctx, good), "issuer revoked the token on exit"))
		}
	}()

	st, err := r.probe(ctx, p, good.Token)
	if !add(expectStatus("accepted", st, err, p.Probe.OKStatus)) {
		return res
	}

	if p.WriteProbe != nil {
		wp := p
		wp.Probe = *p.WriteProbe
		st, err := r.probe(ctx, wp, good.Token)
		add(expectStatus("write_accepted", st, err, p.WriteProbe.OKStatus))
	}

	enforcing := p.enforcing()
	if enforcing && p.Posture == "auto" {
		// A bogus bearer that is admitted shows the target does not check tokens.
		if st, err := r.probe(ctx, p, "connect-matrix.not-a.token"); err == nil && st/100 == 2 {
			enforcing = false
		}
	}
	if !enforcing {
		add(CheckResult{Name: "wrong_audience", Result: Skip, Detail: "target is not enforcing"})
		add(CheckResult{Name: "revoked", Result: Skip, Detail: "target is not enforcing"})
		res.Status = StatusNotEnforced
		return res
	}

	other, err := r.Issuer.Mint(ctx, MintRequest{Name: p.Name + "-other", App: p.Caller, Audience: p.OtherAudience, Scopes: otherScopes(p)})
	if err != nil {
		add(CheckResult{Name: "wrong_audience", Result: Fail, Detail: "could not mint the other-audience token: " + err.Error()})
	} else {
		st, err = r.probe(ctx, p, other.Token)
		add(expectRejected("wrong_audience", st, err))
		add(check("cleanup_other", r.Issuer.Revoke(ctx, other), "issuer revoked the other-audience token"))
	}

	err = r.Issuer.Revoke(ctx, good)
	goodRevoked = err == nil // a failed revoke is retried by the deferred cleanup
	if !add(check("revoke", err, "issuer revoked the token")) {
		return res
	}
	add(r.waitRevoked(ctx, p, good))
	return res
}

// waitRevoked polls until the target refuses the revoked token. It passes only
// if the refusal is observed within the configured wait, measured on the
// runner's clock when the answer arrives; each probe is capped by the time
// left and the wait between probes ends early when ctx is done.
func (r *Runner) waitRevoked(ctx context.Context, p Pair, m Minted) CheckResult {
	start := r.Now()
	bound := r.Cfg.Bound()
	deadline := start.Add(bound)
	lastStatus := 0
	var lastErr error
	for {
		left := deadline.Sub(r.Now())
		if left <= 0 || ctx.Err() != nil {
			detail := fmt.Sprintf("not refused within %s (is the target polling the deny-list successfully?)", bound)
			switch {
			case lastErr != nil:
				detail = "probe error: " + lastErr.Error()
			case lastStatus != 0:
				detail = fmt.Sprintf("still answered %d after %s (is the target polling the deny-list successfully?)", lastStatus, bound)
			}
			return CheckResult{Name: "revoked", Result: Fail, Seconds: r.Now().Sub(start).Seconds(), Detail: detail}
		}
		pctx, cancel := context.WithTimeout(ctx, left)
		st, err := r.probe(pctx, p, m.Token)
		cancel()
		lastStatus, lastErr = st, err
		elapsed := r.Now().Sub(start)
		switch {
		case err == nil && (st == http.StatusUnauthorized || st == http.StatusForbidden):
			if elapsed > bound {
				return CheckResult{Name: "revoked", Result: Fail, Seconds: elapsed.Seconds(),
					Detail: fmt.Sprintf("target answered %d only after %.1fs, over the %s wait", st, elapsed.Seconds(), bound)}
			}
			return CheckResult{Name: "revoked", Result: Pass, Seconds: elapsed.Seconds(), Detail: fmt.Sprintf("target answered %d", st)}
		case elapsed >= bound:
			detail := fmt.Sprintf("still answered %d after %s (is the target polling the deny-list successfully?)", st, bound)
			if err != nil {
				detail = "probe error: " + err.Error()
			}
			return CheckResult{Name: "revoked", Result: Fail, Seconds: elapsed.Seconds(), Detail: detail}
		}
		r.Sleep(ctx, r.Cfg.Poll())
	}
}

// probe makes the pair's request with token ("" = no Authorization header).
// Errors never carry the target URL, which may hold a secret.
func (r *Runner) probe(ctx context.Context, p Pair, token string) (int, error) {
	var body io.Reader
	if p.Probe.Body != "" {
		body = strings.NewReader(p.Probe.Body)
	}
	req, err := http.NewRequestWithContext(ctx, p.Probe.Method, strings.TrimRight(p.Target, "/")+p.Probe.Path, body)
	if err != nil {
		return 0, errors.New("invalid target URL")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.Probe.Host != "" {
		req.Host = p.Probe.Host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.clientFor(p).Do(req) // #nosec G704 -- operator-supplied target
	if err != nil {
		return 0, errors.New(scrub(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// clientFor returns the runner client, or for a pair with a unix socket a
// client of the same settings that dials that socket.
func (r *Runner) clientFor(p Pair) *http.Client {
	if p.UnixSocket == "" {
		return r.Client
	}
	sock := p.UnixSocket
	c := *r.Client
	c.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}, DisableKeepAlives: true}
	return &c
}

func check(name string, err error, ok string) CheckResult {
	if err != nil {
		return CheckResult{Name: name, Result: Fail, Detail: err.Error()}
	}
	return CheckResult{Name: name, Result: Pass, Detail: ok}
}

func expectStatus(name string, got int, err error, want int) CheckResult {
	switch {
	case err != nil:
		return CheckResult{Name: name, Result: Fail, Detail: "probe error: " + err.Error()}
	case got != want:
		return CheckResult{Name: name, Result: Fail, Detail: fmt.Sprintf("target answered %d, want %d", got, want)}
	}
	return CheckResult{Name: name, Result: Pass, Detail: fmt.Sprintf("target answered %d", got)}
}

func expectRejected(name string, got int, err error) CheckResult {
	switch {
	case err != nil:
		return CheckResult{Name: name, Result: Fail, Detail: "probe error: " + err.Error()}
	case got != http.StatusUnauthorized && got != http.StatusForbidden:
		return CheckResult{Name: name, Result: Fail, Detail: fmt.Sprintf("target answered %d, want 401 or 403 (is the target enforcing?)", got)}
	}
	return CheckResult{Name: name, Result: Pass, Detail: fmt.Sprintf("target answered %d", got)}
}

// otherScopes rewrites "<audience>.<verb>" scopes to the other audience, since
// an issuer only grants scopes that belong to the token's audience.
func otherScopes(p Pair) []string {
	out := make([]string, len(p.Scopes))
	for i, s := range p.Scopes {
		if v, ok := strings.CutPrefix(s, p.Audience+"."); ok {
			s = p.OtherAudience + "." + v
		}
		out[i] = s
	}
	return out
}
