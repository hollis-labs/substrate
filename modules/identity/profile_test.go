package identity_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

type profileBody struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Scopes      []string `json:"scopes"`
	Assurance   string   `json:"assurance"`
}

func TestProfileGetReturnsTheSignedInPerson(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("PUT", "/api/identity/profile", map[string]string{"email": "o@example.test"}, cookie(owner)), 200)
	r := a.do("GET", "/api/identity/profile", nil, cookie(owner))
	code(t, r, 200)
	var p profileBody
	r.json(&p)
	if p.ID == "" || p.Username != "owner" || p.DisplayName != "Owner" || p.Email != "o@example.test" || !reflect.DeepEqual(p.Scopes, []string{"*"}) {
		t.Fatalf("profile = %+v", p)
	}
	if p.Assurance != "local" {
		t.Fatalf("assurance = %q, want local", p.Assurance)
	}
	// Exactly these fields, so nothing secret or renamed slips in.
	var raw map[string]any
	r.json(&raw)
	var keys []string
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"assurance", "display_name", "email", "id", "scopes", "username"}) {
		t.Fatalf("fields = %v", keys)
	}
	for _, bad := range []string{"hash", "password", "secret", "$2a$", "$2b$", "disabled"} {
		if strings.Contains(strings.ToLower(r.Body.String()), bad) {
			t.Fatalf("body leaks %q: %s", bad, r.Body)
		}
	}
	// A bearer session for the same person (no cookie) gets the same answer.
	code(t, a.do("GET", "/api/identity/profile", nil, bearer(owner.Value)), 200)
}

func TestProfileGetMatchesTheShapeOfPut(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	var viaPut, viaGet map[string]any
	a.do("PUT", "/api/identity/profile", map[string]string{"display_name": "Chris"}, cookie(owner)).json(&viaPut)
	a.do("GET", "/api/identity/profile", nil, cookie(owner)).json(&viaGet)
	delete(viaGet, "assurance")
	if !reflect.DeepEqual(viaPut, viaGet) {
		t.Fatalf("PUT %v vs GET %v", viaPut, viaGet)
	}
}

func TestProfileGetSignedOutIs401(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	code(t, a.do("GET", "/api/identity/profile", nil), 401)
	code(t, a.do("GET", "/api/identity/profile", nil, bearer("sess_not_real")), 401)
}

func TestProfileGetForAnAPIKeyIsUserRequired(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "svc", "scopes": []string{"demo.read"}})
	r := a.do("GET", "/api/identity/profile", nil, bearer(raw))
	code(t, r, 403)
	if !strings.Contains(r.Body.String(), `"user_required"`) {
		t.Fatalf("body = %s", r.Body)
	}
	// A key holding everything still has no person behind it.
	all, _ := a.key(owner, map[string]any{"name": "root", "scopes": []string{"*"}})
	code(t, a.do("GET", "/api/identity/profile", nil, bearer(all)), 403)
}

func TestProfileGetForATokenMintedFromAKeyIsUserRequired(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "svc", "scopes": []string{"demo.read"}})
	var tok struct {
		Token string `json:"token"`
	}
	m := a.do("POST", "/api/token", map[string]any{}, bearer(raw))
	code(t, m, 200)
	m.json(&tok)
	code(t, a.do("GET", "/api/identity/profile", nil, bearer(tok.Token)), 403)
}

func TestProfileGetForADisabledUserIsRefused(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "display_name": "Ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 201)
	ann := a.login("ann", password).cookie("demo_session")
	code(t, a.do("GET", "/api/identity/profile", nil, cookie(ann)), 200)
	// A short-lived token minted by ann outlives her sessions being ended, so the
	// handler itself must refuse a disabled account.
	var tok struct {
		Token string `json:"token"`
	}
	m := a.do("POST", "/api/token", map[string]any{}, cookie(ann))
	code(t, m, 200)
	m.json(&tok)
	code(t, a.do("GET", "/api/identity/profile", nil, bearer(tok.Token)), 200)
	code(t, a.do("PUT", "/api/identity/users/"+userID(t, a, owner, "ann"), map[string]any{"disabled": true}, cookie(owner)), 200)
	if got := a.do("GET", "/api/identity/profile", nil, cookie(ann)).Code; got != 401 && got != 403 {
		t.Fatalf("disabled user got %d, want 401 or 403", got)
	}
	r := a.do("GET", "/api/identity/profile", nil, bearer(tok.Token))
	code(t, r, 403)
	if !strings.Contains(r.Body.String(), `"user_required"`) {
		t.Fatalf("body = %s", r.Body)
	}
}

func TestProfileGetScopesAreACopyAndPerUser(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 201)
	ann := a.login("ann", password).cookie("demo_session")
	var p profileBody
	a.do("GET", "/api/identity/profile", nil, cookie(ann)).json(&p)
	if p.Username != "ann" || !reflect.DeepEqual(p.Scopes, []string{"demo.read"}) {
		t.Fatalf("profile = %+v", p)
	}
	// Mutating a response must not reach stored scopes (each GET is a fresh copy).
	p.Scopes[0] = "*"
	var again profileBody
	a.do("GET", "/api/identity/profile", nil, cookie(ann)).json(&again)
	if !reflect.DeepEqual(again.Scopes, []string{"demo.read"}) {
		t.Fatalf("scopes changed: %v", again.Scopes)
	}
	var op profileBody
	a.do("GET", "/api/identity/profile", nil, cookie(owner)).json(&op)
	if op.Username != "owner" {
		t.Fatalf("owner saw %+v", op)
	}
}
