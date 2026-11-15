package identity_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/substrate/modules/mcp"
)

// ownerKey returns a key made with Service.CreateKey, the way
// a process that mints per-child keys does.
func (a *app) ownerKey(name string, ttl time.Duration, scopes ...string) (raw, id string) {
	a.t.Helper()
	key, cred, err := a.svc.CreateKey(context.Background(), identity.KeyInput{Name: name, Scopes: scopes, TTL: ttl})
	if err != nil {
		a.t.Fatal(err)
	}
	return key, cred.ID
}

func TestRevokeKeyRefusesTheKeyAtOnceOverHTTP(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	ctx := context.Background()
	raw, id := a.ownerKey("child-1", 0, "demo.read")
	other, otherID := a.ownerKey("child-2", 0, "demo.read")

	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 200)
	if err := a.svc.RevokeKey(ctx, id); err != nil {
		t.Fatal(err)
	}
	// No sleep, no sweep: the very next request is refused.
	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 401)
	// A sibling key is untouched.
	code(t, a.do("GET", "/api/me", nil, bearer(other)), 200)
	// The key is gone from the list the administrator sees; the sibling is not.
	list := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	code(t, list, 200)
	if strings.Contains(list.Body.String(), id) || !strings.Contains(list.Body.String(), otherID) {
		t.Fatalf("keys after revoke = %s", list.Body)
	}
}

func TestRevokeKeyIsIdempotentAndKnowsUnknownIds(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	ctx := context.Background()
	_, id := a.ownerKey("child", 0, "demo.read")

	for i := 0; i < 3; i++ {
		if err := a.svc.RevokeKey(ctx, id); err != nil {
			t.Fatalf("revoke #%d of the same key: %v", i+1, err)
		}
	}
	for _, bad := range []string{"k_does_not_exist", "", "  ", "key_" + id} {
		if err := a.svc.RevokeKey(ctx, bad); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("RevokeKey(%q) = %v, want ErrNotFound", bad, err)
		}
	}
	// A session id is not a key id.
	var sessions struct {
		Sessions []struct{ ID string }
	}
	r := a.do("GET", "/api/identity/sessions", nil, cookie(owner))
	code(t, r, 200)
	r.json(&sessions)
	if len(sessions.Sessions) == 0 {
		t.Fatal("no session to try")
	}
	if err := a.svc.RevokeKey(ctx, sessions.Sessions[0].ID); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("RevokeKey(session id) = %v, want ErrNotFound", err)
	}
	code(t, a.do("GET", "/api/identity/sessions", nil, cookie(owner)), 200) // the session was not revoked
}

// The HTTP handler and the Go method are one path: a repeat DELETE answers 204 as
// RevokeKey answers nil, an unknown id 404 as it answers ErrNotFound.
func TestDeleteKeyHandlerUsesTheSameRevokePath(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, id := a.ownerKey("child", 0, "demo.read")

	code(t, a.do("DELETE", "/api/identity/keys/"+id, nil, cookie(owner)), 204)
	code(t, a.do("DELETE", "/api/identity/keys/"+id, nil, cookie(owner)), 204)
	code(t, a.do("DELETE", "/api/identity/keys/k_nope", nil, cookie(owner)), 404)
	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 401)
	// Revoked through the method first, the HTTP DELETE is still 204.
	raw2, id2 := a.ownerKey("child2", 0, "demo.read")
	if err := a.svc.RevokeKey(context.Background(), id2); err != nil {
		t.Fatal(err)
	}
	code(t, a.do("DELETE", "/api/identity/keys/"+id2, nil, cookie(owner)), 204)
	code(t, a.do("GET", "/api/me", nil, bearer(raw2)), 401)
	// Without the right scope the route is still guarded.
	other, _ := a.ownerKey("reader", 0, "demo.read")
	code(t, a.do("DELETE", "/api/identity/keys/"+id, nil, bearer(other)), 403)
}

// An expired key is refused by its own expiry, read on every verification: no
// sweep has to run (the module's hourly sweep never touches API keys, and the
// row is still there, still listed).
func TestExpiredKeyIsRefusedWithoutASweep(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, id := a.ownerKey("short", 2*time.Second, "demo.read")
	forever, _ := a.ownerKey("forever", 0, "demo.read")

	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 200)
	time.Sleep(3100 * time.Millisecond)
	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 401)
	code(t, a.do("GET", "/api/me", nil, bearer(forever)), 200) // no TTL, no expiry
	// Still stored, still listed: nothing removed it.
	list := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	code(t, list, 200)
	if !strings.Contains(list.Body.String(), id) {
		t.Fatalf("the expired key row is gone (a sweep ran?): %s", list.Body)
	}
	var n int
	if err := a.svc.DBForTest().QueryRow(`SELECT COUNT(*) FROM identity_credentials WHERE id = ?`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expired key row count = %d, %v", n, err)
	}
	// It cannot be used to mint a token either.
	code(t, a.do("POST", "/api/token", map[string]any{}, bearer(raw)), 401)
}

// ---- the stdio path ----

func startWithMCP(t *testing.T) (*app, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
		"database": {"sqlite": {"path": %q}},
		"auth": {"mode": "enforce"},
		"identity": {"key_file": %q, "bcrypt_cost": 4, "setup": "http"}
	}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity", "mcp"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	mcp.Server(run.App).RegisterChecked(gmcp.Tool{
		Name: "ping", Description: "returns pong", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(context.Context, map[string]any) (any, error) { return map[string]any{"msg": "pong"}, nil },
	}, gmcp.Reads("constant"))
	svc := identity.From(run.App)
	a := &app{t: t, dir: dir, svc: svc, running: run}
	_, _, err = svc.CreateKey(context.Background(), identity.KeyInput{Name: "x", Scopes: []string{"x"}})
	if !errors.Is(err, identity.ErrNoOwner) {
		t.Fatalf("expected ErrNoOwner before setup, got %v", err)
	}
	if _, err := svc.SetupOwner(context.Background(), identity.OwnerInput{Username: "owner", Password: password}); err != nil {
		t.Fatal(err)
	}
	return a, dir
}

func writeTok(t *testing.T, tok string) string {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// serveStdio starts mcp.ServeStdio with the token file and a client; it returns the
// start error when the token is refused.
func serveStdio(t *testing.T, a *app, tokenFile string) (*mcpsdk.ClientSession, error) {
	t.Helper()
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		err := mcp.ServeStdio(ctx, a.running.App, mcp.StdioOptions{In: srvIn, Out: srvOut, TokenFile: tokenFile})
		_ = srvOut.Close()
		_ = srvIn.Close()
		done <- err
	}()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).
		Connect(ctx, &mcpsdk.IOTransport{Reader: cliIn, Writer: cliOut}, nil)
	if err != nil {
		select {
		case serr := <-done:
			if serr != nil {
				return nil, serr
			}
		case <-time.After(2 * time.Second):
		}
		return nil, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, nil
}

func TestRevokedAndExpiredKeysAreRefusedOverStdio(t *testing.T) {
	a, _ := startWithMCP(t)
	raw, id := a.ownerKey("child", 0, "demo.mcp")
	tok := writeTok(t, raw)

	cs, err := serveStdio(t, a, tok)
	if err != nil {
		t.Fatalf("a live key was refused over stdio: %v", err)
	}
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "ping"})
	if err != nil || res.IsError {
		t.Fatalf("ping with a live key: %v %+v", err, res)
	}
	_ = cs.Close()

	if err := a.svc.RevokeKey(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// The next start with the same token file is refused at once.
	if _, err := serveStdio(t, a, tok); err == nil || !strings.Contains(err.Error(), "was not accepted") {
		t.Fatalf("a revoked key started a stdio process: %v", err)
	}

	// Expired, with no sweep: refused the same way.
	short, _ := a.ownerKey("short", 2*time.Second, "demo.mcp")
	stok := writeTok(t, short)
	if cs, err := serveStdio(t, a, stok); err != nil {
		t.Fatalf("a live short key was refused: %v", err)
	} else {
		_ = cs.Close()
	}
	time.Sleep(3100 * time.Millisecond)
	if _, err := serveStdio(t, a, stok); err == nil || !strings.Contains(err.Error(), "was not accepted") {
		t.Fatalf("an expired key started a stdio process: %v", err)
	}
}
