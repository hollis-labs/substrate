package mcp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/mcp"
)

// whoamiTool reports the principal a handler sees.
func whoamiTool(app *module.App) {
	mcp.Server(app).RegisterChecked(gmcp.Tool{
		Name: "whoami", Description: "the caller", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			p, ok := libauth.FromContext(ctx)
			return map[string]any{"found": ok, "id": p.ID, "kind": p.Kind, "verified": p.Verified}, nil
		},
	}, gmcp.Reads("constant"))
}

func empty() mcp.StdioOptions {
	return mcp.StdioOptions{In: io.NopCloser(strings.NewReader("")), Out: nopWriteCloser{io.Discard}, Notice: io.Discard}
}

func TestLocalTrustOffKeepsEnforceRequiringAToken(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	o := empty()
	o.LocalScopes = []string{"*"} // narrowing alone does not enable it
	err := mcp.ServeStdio(context.Background(), app, o)
	if err == nil || !strings.Contains(err.Error(), "needs a token") {
		t.Fatalf("without LocalTrust: %v", err)
	}
}

func TestLocalTrustRunsUnderTheLocalPrincipal(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	whoamiTool(app)
	var notice strings.Builder
	cs, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, Notice: &notice})
	if out, isErr := callTool(t, cs, "ping"); isErr || !strings.Contains(out, "pong") {
		t.Fatalf("ping: %q err=%v", out, isErr)
	}
	out, isErr := callTool(t, cs, "whoami")
	if isErr || !strings.Contains(out, `"id":"local:operator"`) || !strings.Contains(out, `"kind":"operator"`) || !strings.Contains(out, `"verified":true`) {
		t.Fatalf("whoami: %q err=%v", out, isErr)
	}
	// The default scope is every scope, so a per-tool scope passes.
	if out, isErr := callTool(t, cs, "secret"); isErr || !strings.Contains(out, "classified") {
		t.Fatalf("secret under default scopes: %q err=%v", out, isErr)
	}
	if !strings.Contains(notice.String(), "local filesystem trust") || !strings.Contains(notice.String(), mcp.LocalPrincipalID) {
		t.Fatalf("notice: %q", notice.String())
	}
}

func TestLocalTrustNarrowedScopesStillAuthorizePerTool(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	cs, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, LocalScopes: []string{"t.mcp"}, Notice: io.Discard})
	if out, isErr := callTool(t, cs, "ping"); isErr || !strings.Contains(out, "pong") {
		t.Fatalf("ping: %q err=%v", out, isErr)
	}
	if out, isErr := callTool(t, cs, "secret"); !isErr {
		t.Fatalf("a local principal without t.admin reached the tool: %q", out)
	}
	cs2, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, LocalScopes: []string{"t.mcp", "t.admin"}, Notice: io.Discard})
	if out, isErr := callTool(t, cs2, "secret"); isErr || !strings.Contains(out, "classified") {
		t.Fatalf("secret with t.admin: %q err=%v", out, isErr)
	}
}

func TestLocalTrustNeverReplacesAGivenToken(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	whoamiTool(app)
	// A good token wins: the principal is the key's, and a read key stays narrow.
	cs, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, TokenFile: tokenFile(t, readKey), Notice: io.Discard})
	out, _ := callTool(t, cs, "whoami")
	if !strings.Contains(out, "svc/r") || strings.Contains(out, "local:operator") {
		t.Fatalf("whoami with a token: %q", out)
	}
	if out, isErr := callTool(t, cs, "secret"); !isErr {
		t.Fatalf("LocalTrust widened a token: %q", out)
	}
	// A bad token is an error with no fallback.
	for name, path := range map[string]string{"unknown": tokenFile(t, "not-a-key"), "empty": tokenFile(t, ""), "missing": filepath.Join(t.TempDir(), "absent")} {
		o := empty()
		o.LocalTrust, o.TokenFile = true, path
		if err := mcp.ServeStdio(context.Background(), app, o); err == nil {
			t.Errorf("%s: fell back to local trust", name)
		}
	}
}

func TestLocalTrustIsNotReadFromConfig(t *testing.T) {
	app := stdioApp(t, strings.TrimSuffix(authCfg("enforce"), "}")+`,"mcp":{"local_trust":true}}`)
	if err := mcp.ServeStdio(context.Background(), app, empty()); err == nil {
		t.Fatal("a config key enabled local trust")
	}
}

func TestLocalTrustPermissiveStampsThePrincipalAndOffIsUnchanged(t *testing.T) {
	app := stdioApp(t, authCfg("permissive"))
	whoamiTool(app)
	cs, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, Notice: io.Discard})
	if out, _ := callTool(t, cs, "whoami"); !strings.Contains(out, "local:operator") {
		t.Fatalf("permissive: %q", out)
	}
	t.Setenv("HOLLIS_AUTH", "")
	off := stdioApp(t, `{}`)
	whoamiTool(off)
	var notice strings.Builder
	cs2, _ := connectWith(t, off, mcp.StdioOptions{LocalTrust: true, Notice: &notice})
	if out, _ := callTool(t, cs2, "whoami"); strings.Contains(out, "local:operator") || notice.Len() != 0 {
		t.Fatalf("off mode stamped a local principal: %q %q", out, notice.String())
	}
}

func TestHTTPMCPStaysEnforcedBesideLocalTrust(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	mnt, _ := app.Value(mcp.MountKey)
	handler := mnt.(interface{ MCPHandler() http.Handler }).MCPHandler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("HTTP /mcp without credentials: %d", rec.Code)
	}
}

// An app with a scope no same-host agent may hold passes LocalScopes without it;
// the tool mapped to that scope is then refused, and allowed once it is listed.
func TestLocalScopesWithoutAScopeRefuseAToolMappedToIt(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	const guarded = "secret" // mapped to t.admin
	without, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, LocalScopes: []string{"t.mcp"}, Notice: io.Discard})
	if out, isErr := callTool(t, without, guarded); !isErr || strings.Contains(out, "classified") {
		t.Fatalf("LocalScopes without t.admin reached the t.admin tool: %q err=%v", out, isErr)
	}
	with, _ := connectWith(t, app, mcp.StdioOptions{LocalTrust: true, LocalScopes: []string{"t.mcp", "t.admin"}, Notice: io.Discard})
	if out, isErr := callTool(t, with, guarded); isErr || !strings.Contains(out, "classified") {
		t.Fatalf("LocalScopes with t.admin was refused: %q err=%v", out, isErr)
	}
}
