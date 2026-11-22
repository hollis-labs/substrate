package identity_test

import (
	"testing"
)

func TestMyKeys(t *testing.T) {
	a := start(t, "enforce", "")
	own := cookie(a.owner())
	w := a.do("POST", "/api/identity/users", map[string]any{"username": "sam", "password": password, "role": "admin"}, own)
	code(t, w, 201)
	var sam struct {
		ID string `json:"id"`
	}
	w.json(&sam)
	w = a.login("sam", password)
	code(t, w, 200)
	samc := cookie(w.cookie("demo_session"))

	// Sam makes a key with scopes Sam holds; the secret comes back once.
	type made struct {
		Key        string `json:"key"`
		Credential struct {
			ID        string `json:"id"`
			CreatedBy string `json:"created_by"`
		} `json:"credential"`
	}
	var k made
	w = a.do("POST", "/api/identity/me/keys", map[string]any{"name": "laptop agent", "scopes": []string{"demo.admin"}, "ttl_hours": 24}, samc)
	code(t, w, 201)
	w.json(&k)
	if k.Key == "" || k.Credential.CreatedBy != "user:"+sam.ID {
		t.Fatalf("made %+v", k)
	}
	code(t, a.do("POST", "/api/identity/me/keys", map[string]any{"name": "too much", "scopes": []string{"*"}}, samc), 400)
	code(t, a.do("POST", "/api/identity/me/keys", map[string]any{"name": "forever and a day", "scopes": []string{"demo.read"}, "ttl_hours": 9000}, samc), 400)
	// The key works with its scopes, and acts for Sam (Principal.Parent).
	code(t, a.do("GET", "/api/identity/users", nil, bearer(k.Key)), 200)
	if p, err := a.svc.Verify(t.Context(), k.Key); err != nil || p.Parent != "user:"+sam.ID || p.ID != "key:"+k.Credential.ID {
		t.Fatalf("principal %+v %v", p, err)
	}
	// A key cannot make or list keys.
	code(t, a.do("GET", "/api/identity/me/keys", nil, bearer(k.Key)), 403)
	code(t, a.do("POST", "/api/identity/me/keys", map[string]any{"name": "x", "scopes": []string{"demo.read"}}, bearer(k.Key)), 403)

	// Each person sees their own; the owner's admin list sees all.
	var owners made
	a.do("POST", "/api/identity/me/keys", map[string]any{"name": "owner tool", "scopes": []string{"demo.read"}}, own).json(&owners)
	var list struct {
		Keys []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"keys"`
	}
	a.do("GET", "/api/identity/me/keys", nil, samc).json(&list)
	if len(list.Keys) != 1 || list.Keys[0].Name != "laptop agent" {
		t.Fatalf("sam's keys %+v", list)
	}
	a.do("GET", "/api/identity/keys", nil, own).json(&list)
	if len(list.Keys) != 2 {
		t.Fatalf("all keys %+v", list)
	}
	// Sam cannot revoke the owner's key through the self-service route.
	code(t, a.do("DELETE", "/api/identity/me/keys/"+owners.Credential.ID, nil, samc), 404)

	// Demoted to member: the key keeps only what Sam still holds.
	code(t, a.do("PUT", "/api/identity/users/"+sam.ID, map[string]any{"role": "member"}, own), 200)
	code(t, a.do("GET", "/api/identity/users", nil, bearer(k.Key)), 403)
	// Disabled: the key stops.
	code(t, a.do("PUT", "/api/identity/users/"+sam.ID, map[string]any{"disabled": true}, own), 200)
	code(t, a.do("GET", "/api/identity/users", nil, bearer(k.Key)), 401)
	code(t, a.do("PUT", "/api/identity/users/"+sam.ID, map[string]any{"disabled": false, "role": "admin"}, own), 200)
	code(t, a.do("GET", "/api/identity/users", nil, bearer(k.Key)), 200)

	// Revoked by its maker, it stops.
	w = a.login("sam", password)
	samc = cookie(w.cookie("demo_session"))
	code(t, a.do("DELETE", "/api/identity/me/keys/"+k.Credential.ID, nil, samc), 204)
	code(t, a.do("GET", "/api/identity/users", nil, bearer(k.Key)), 401)
}
