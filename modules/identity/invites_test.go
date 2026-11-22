package identity_test

import (
	"slices"
	"testing"
)

func TestInvites(t *testing.T) {
	a := start(t, "enforce", "")
	own := a.owner()
	page := header("X-Requested-With", "x")

	// The owner invites a member; the code comes back once.
	w := a.do("POST", "/api/identity/invites", map[string]any{"role": "member", "label": "Sam"}, cookie(own))
	code(t, w, 201)
	var inv struct {
		ID, Code, Role string
	}
	w.json(&inv)
	if inv.Role != "member" || len(inv.Code) < 20 {
		t.Fatalf("invite %+v", inv)
	}
	var list struct {
		Invites []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"invites"`
	}
	a.do("GET", "/api/identity/invites", nil, cookie(own)).json(&list)
	if len(list.Invites) != 1 || list.Invites[0].Label != "Sam" {
		t.Fatalf("pending %+v", list)
	}

	// Anyone with the link can see what it is for, and accept it once.
	w = a.do("POST", "/api/invite/check", map[string]string{"code": inv.Code})
	code(t, w, 200)
	var chk struct{ Role, Label string }
	w.json(&chk)
	if chk.Role != "member" || chk.Label != "Sam" {
		t.Fatalf("check %+v", chk)
	}
	accept := map[string]string{"code": inv.Code, "username": "sam", "display_name": "Sam", "password": password}
	code(t, a.do("POST", "/api/invite/accept", accept), 403) // no X-Requested-With
	w = a.do("POST", "/api/invite/accept", accept, page)
	code(t, w, 200)
	sam := w.cookie("demo_session")
	if sam == nil {
		t.Fatal("accepting set no session")
	}
	role, scopes := profileOf(t, a, cookie(sam))
	if role != "member" || !slices.Contains(scopes, "demo.use") || slices.Contains(scopes, "demo.admin") {
		t.Fatalf("joined as %q %v", role, scopes)
	}
	accept["username"] = "sam2"
	code(t, a.do("POST", "/api/invite/accept", accept, page), 403) // used
	code(t, a.do("POST", "/api/invite/check", map[string]string{"code": inv.Code}), 404)
	a.do("GET", "/api/identity/invites", nil, cookie(own)).json(&list)
	if len(list.Invites) != 0 {
		t.Fatalf("a used invite is still pending: %+v", list)
	}

	// A member cannot invite; an admin cannot invite an owner.
	code(t, a.do("POST", "/api/identity/invites", map[string]any{"role": "guest"}, cookie(sam)), 403)
	w = a.do("POST", "/api/identity/invites", map[string]any{"role": "admin"}, cookie(own))
	code(t, w, 201)
	var adminInv struct{ Code string }
	w.json(&adminInv)
	w = a.do("POST", "/api/invite/accept", map[string]string{"code": adminInv.Code, "username": "ops", "password": password}, page)
	code(t, w, 200)
	ops := w.cookie("demo_session")
	code(t, a.do("POST", "/api/identity/invites", map[string]any{"role": "owner"}, cookie(ops)), 400)

	// Revoked invites stop working.
	w = a.do("POST", "/api/identity/invites", map[string]any{"role": "guest", "ttl_hours": 1}, cookie(ops))
	code(t, w, 201)
	var g struct{ ID, Code string }
	w.json(&g)
	code(t, a.do("DELETE", "/api/identity/invites/"+g.ID, nil, cookie(ops)), 204)
	code(t, a.do("POST", "/api/invite/check", map[string]string{"code": g.Code}), 404)
	code(t, a.do("DELETE", "/api/identity/invites/nope", nil, cookie(ops)), 404)
	code(t, a.do("POST", "/api/identity/invites", map[string]any{"role": "guest", "ttl_hours": 24 * 31}, cookie(ops)), 400)
}
