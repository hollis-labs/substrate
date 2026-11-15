package folio_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio/service"
)

func TestAppChat_BuildsAndIsolatesOwners(t *testing.T) {
	version := os.Getenv("FOLIO_SUBSTRATE_VERSION")
	if version == "" {
		if os.Getenv("FOLIO_REQUIRE_TOOLS") == "1" {
			t.Fatal("FOLIO_SUBSTRATE_VERSION is required")
		}
		t.Skip("SKIPPED, not passed: FOLIO_SUBSTRATE_VERSION is unset")
	}
	if !needTool(t, "go") || !needTool(t, "npm") {
		return
	}
	kits := bomKits(t, version)
	inputs := map[string]any{"repo_name": "chatty", "substrate_version": version, "http_port": "18218"}
	for _, kit := range []string{"app-shell", "design-app-runtime", "design-bindings", "design-components", "design-tokens", "kit-dashboard", "plugin-host-ui", "kit-chat", "kit-cards", "kit-identity", "kit-account"} {
		inputs["kit_"+strings.ReplaceAll(strings.TrimPrefix(kit, "kit-"), "-", "_")] = kits[kit]
	}
	dir := filepath.Join(t.TempDir(), "chatty")
	if review := os.Getenv("FOLIO_CHAT_REVIEW_DIR"); review != "" {
		dir = review
	}
	_, err := newGoLibService(t).New(service.NewOptions{PresetID: "app-chat", TargetDir: dir, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "go", "mod", "tidy")
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "pins", "sync", "--app", ".")
	mustRun(t, dir, "go", "mod", "tidy")
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "pins", "check", "--app", ".")
	fe := filepath.Join(dir, "frontend")
	mustRun(t, fe, "npm", "install", "--no-audit", "--no-fund")
	mustRun(t, fe, "npm", "run", "typecheck")
	mustRun(t, fe, "npm", "run", "lint")
	mustRun(t, fe, "npm", "run", "build")
	mustRun(t, dir, "go", "vet", "./...")
	mustRun(t, dir, "go", "test", "-race", "./...")
	if needTool(t, "golangci-lint") {
		mustRun(t, dir, "golangci-lint", "run", "./...")
	}
	checkTransportGate(t, dir)
	bin := filepath.Join(dir, "chatty")
	mustRun(t, dir, "go", "build", "-o", bin, "./cmd/chatty")
	driveSubstrateBinary(t, bin, "chatty", "18218", func(owner *http.Client, base string) { checkChatOwners(t, owner, base) })
	serveAndFetchSPA(t, bin, "chatty", "18218")
}

func checkChatOwners(t *testing.T, owner *http.Client, base string) {
	t.Helper()
	call := func(client *http.Client, method, path string, input any, want int) []byte {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, base+path, strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "test")
		req.Header.Set("Origin", base)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, out, want)
		}
		return out
	}
	password := randomHex(t)
	call(owner, "POST", "/api/identity/users", map[string]any{"username": "second", "password": password, "scopes": []string{"chatty.read", "chatty.write"}}, 201)
	jar, _ := cookiejar.New(nil)
	second := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	call(second, "POST", "/api/login", map[string]string{"username": "second", "password": password}, 200)
	call(&http.Client{Timeout: 10 * time.Second}, "GET", "/api/conversations", nil, 401)
	create := func(client *http.Client, title string) int64 {
		t.Helper()
		var result struct{ ID int64 }
		body := call(client, "POST", "/api/conversations", map[string]string{"title": title}, 201)
		if err := json.Unmarshal(body, &result); err != nil || result.ID < 1 {
			t.Fatalf("create: %s, %v", body, err)
		}
		return result.ID
	}
	firstID, secondID := create(owner, "first owner's conversation"), create(second, "second owner's conversation")
	path := func(id int64) string { return fmt.Sprintf("/api/conversations/%d/messages", id) }
	for _, pair := range []struct {
		mine, other *http.Client
		id          int64
		content     string
	}{{owner, second, firstID, "private first message"}, {second, owner, secondID, "private second message"}} {
		call(pair.mine, "POST", path(pair.id), map[string]any{"content": "spoof", "owner": "someone-else"}, 400)
		call(pair.mine, "POST", path(pair.id), map[string]any{"content": "spoof", "role": "assistant"}, 400)
		saved := call(pair.mine, "POST", path(pair.id), map[string]string{"content": pair.content}, 201)
		if !strings.Contains(string(saved), `"assistant_connected":false`) {
			t.Fatalf("unconfigured assistant: %s", saved)
		}
		call(pair.other, "GET", path(pair.id), nil, 404)
		call(pair.other, "POST", path(pair.id), map[string]string{"content": "intrusion"}, 404)
		var got struct {
			Messages []struct{ Content, Role string }
		}
		data := call(pair.mine, "GET", path(pair.id), nil, 200)
		if err := json.Unmarshal(data, &got); err != nil || len(got.Messages) != 1 || got.Messages[0].Content != pair.content || got.Messages[0].Role != "user" {
			t.Fatalf("persisted owner history: %s, %v", data, err)
		}
		var listed struct{ Conversations []struct{ ID int64 } }
		data = call(pair.mine, "GET", "/api/conversations", nil, 200)
		if err := json.Unmarshal(data, &listed); err != nil || len(listed.Conversations) != 1 || listed.Conversations[0].ID != pair.id {
			t.Fatalf("owner list: %s, %v", data, err)
		}
	}
}
