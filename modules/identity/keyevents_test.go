package identity_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/substrate/modules/identity"
)

func TestWatchKeysReportsEveryMakeAndRealRevoke(t *testing.T) {
	a := start(t, "enforce", "")
	own := cookie(a.owner())
	var mu sync.Mutex
	var got []string
	stop := a.svc.WatchKeys(func(e identity.KeyEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e.Action+" "+e.Key.Name+" by="+strings.SplitN(e.By, ":", 2)[0]+" maker="+strings.SplitN(e.Key.CreatedBy, ":", 2)[0])
	})

	// The admin route, the self-service route, a redeemed pairing code, and in-process CreateKey.
	var admin struct {
		Credential struct{ ID string } `json:"credential"`
	}
	w := a.do("POST", "/api/identity/keys", map[string]any{"name": "ci", "scopes": []string{"demo.read"}}, own)
	code(t, w, 201)
	w.json(&admin)
	var mine struct {
		Credential struct{ ID string } `json:"credential"`
	}
	w = a.do("POST", "/api/identity/me/keys", map[string]any{"name": "laptop", "scopes": []string{"demo.read"}}, own)
	code(t, w, 201)
	w.json(&mine)
	var pc struct{ Code string }
	a.do("POST", "/api/identity/pairing-codes", map[string]any{"name": "paired", "scopes": []string{"demo.read"}, "ttl_hours": 1}, own).json(&pc)
	code(t, a.do("POST", "/api/pair", map[string]string{"code": pc.Code}), 201)
	_, childID := a.ownerKey("child", 0, "demo.read")

	// Revokes: by route, again (already revoked: no event), self-service, in-process; an
	// unknown id is not an event either.
	code(t, a.do("DELETE", "/api/identity/keys/"+admin.Credential.ID, nil, own), 204)
	code(t, a.do("DELETE", "/api/identity/keys/"+admin.Credential.ID, nil, own), 204)
	code(t, a.do("DELETE", "/api/identity/me/keys/"+mine.Credential.ID, nil, own), 204)
	if err := a.svc.RevokeKey(context.Background(), childID); err != nil {
		t.Fatal(err)
	}
	code(t, a.do("DELETE", "/api/identity/keys/k_nope", nil, own), 404)

	stop()
	a.ownerKey("after stop", 0, "demo.read")

	want := []string{
		"created ci by=user maker=user",
		"created laptop by=user maker=user",
		"created paired by=user maker=user",
		"created child by=local maker=local",
		"revoked ci by=user maker=user",
		"revoked laptop by=user maker=user",
		"revoked child by= maker=local",
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s", strings.Join(got, "\n"))
	}
}
