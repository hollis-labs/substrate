package identity_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/hollis-labs/substrate/modules/identity"
)

// OwnerExists is what GET /api/setup answers (setup_required is its negation), before and
// after the owner, and CreateKey refuses until it is true.
func TestOwnerExistsMatchesTheSetupRoute(t *testing.T) {
	a := start(t, "enforce", "")
	ctx := context.Background()
	setupRequired := func() bool {
		w := a.do("GET", "/api/setup", nil)
		code(t, w, 200)
		var b struct {
			SetupRequired bool `json:"setup_required"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b.SetupRequired
	}
	if ok, err := a.svc.OwnerExists(ctx); err != nil || ok {
		t.Fatalf("before setup: %v %v", ok, err)
	}
	if !setupRequired() {
		t.Fatal("GET /api/setup says no setup needed before the owner")
	}
	if _, _, err := a.svc.CreateKey(ctx, identity.KeyInput{Name: "k", Scopes: []string{"demo.read"}}); err != identity.ErrNoOwner {
		t.Fatalf("CreateKey before the owner: %v", err)
	}
	a.owner()
	if ok, err := a.svc.OwnerExists(ctx); err != nil || !ok {
		t.Fatalf("after setup: %v %v", ok, err)
	}
	if setupRequired() {
		t.Fatal("GET /api/setup still asks for setup")
	}
}

// ListKeys is the list GET /api/identity/keys serves: the same ids in the same order, revoked
// keys left out, and an empty list (not nil) when there are none.
func TestListKeysMatchesTheKeysRoute(t *testing.T) {
	a := start(t, "enforce", "")
	ctx := context.Background()
	owner := a.owner()
	keys, err := a.svc.ListKeys(ctx)
	if err != nil || keys == nil || len(keys) != 0 {
		t.Fatalf("no keys: %#v %v", keys, err)
	}
	_, id1 := a.ownerKey("one", 0, "demo.read")
	_, id2 := a.ownerKey("two", 0, "demo.read")
	_, id3 := a.ownerKey("three", 0, "demo.write")
	if err := a.svc.RevokeKey(ctx, id2); err != nil {
		t.Fatal(err)
	}
	keys, err = a.svc.ListKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range keys {
		got = append(got, k.ID)
	}
	w := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	code(t, w, 200)
	var body struct {
		Keys []identity.Credential `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var route []string
	for _, k := range body.Keys {
		route = append(route, k.ID)
	}
	// Keys made in the same second tie on created_at and are then ordered by id, so compare
	// the set here and the order against the route below.
	if len(got) != 2 || !slices.Contains(got, id1) || !slices.Contains(got, id3) {
		t.Fatalf("ListKeys = %v, want %s and %s (not the revoked %s)", got, id1, id3, id2)
	}
	if len(route) != len(got) || route[0] != got[0] || route[1] != got[1] {
		t.Fatalf("route %v differs from ListKeys %v", route, got)
	}
}
