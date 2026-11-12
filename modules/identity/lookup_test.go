package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/substrate/modules/identity"
)

func userID(t *testing.T, a *app, owner *http.Cookie, name string) string {
	t.Helper()
	var users struct {
		Users []struct{ ID, Username string }
	}
	r := a.do("GET", "/api/identity/users", nil, cookie(owner))
	r.json(&users)
	for _, u := range users.Users {
		if u.Username == name {
			return u.ID
		}
	}
	t.Fatalf("no user %s", name)
	return ""
}

func TestUserLookupReturnsThePublicView(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "display_name": "Ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 201)
	id := userID(t, a, owner, "ann")
	ann := a.login("ann", password).cookie("demo_session")
	code(t, a.do("PUT", "/api/identity/profile", map[string]string{"email": "ann@example.test"}, cookie(ann)), 200)

	got, err := a.svc.User(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := identity.PublicUser{ID: id, Username: "ann", DisplayName: "Ann", Email: "ann@example.test", Scopes: []string{"demo.read"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Mutating the result must not reach the service's data.
	got.Scopes[0] = "*"
	again, _ := a.svc.User(context.Background(), id)
	if again.Scopes[0] != "demo.read" {
		t.Fatal("scopes alias internal state")
	}
}

func TestUserLookupUnknownIDIsNotFound(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	for _, id := range []string{"", "usr_nope", "x' OR '1'='1"} {
		if _, err := a.svc.User(context.Background(), id); !errors.Is(err, identity.ErrNotFound) {
			t.Fatalf("id %q: err %v, want ErrNotFound", id, err)
		}
	}
}

func TestUserLookupReportsDisabledAccounts(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 201)
	id := userID(t, a, owner, "ann")
	code(t, a.do("PUT", "/api/identity/users/"+id, map[string]any{"disabled": true}, cookie(owner)), 200)
	u, err := a.svc.User(context.Background(), id)
	if err != nil || !u.Disabled {
		t.Fatalf("got %+v, %v; want a disabled user", u, err)
	}
	code(t, a.do("PUT", "/api/identity/users/"+id, map[string]any{"disabled": false}, cookie(owner)), 200)
	if u, _ = a.svc.User(context.Background(), id); u.Disabled {
		t.Fatal("still disabled after re-enable")
	}
}

func TestUserLookupViewHasNoSecretFields(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	id := userID(t, a, owner, "owner")
	u, _ := a.svc.User(context.Background(), id)
	b, _ := json.Marshal(u)
	for _, bad := range []string{"hash", "password", "secret", "token", "$2a$", "$2b$"} {
		if strings.Contains(strings.ToLower(string(b)), bad) {
			t.Fatalf("view leaks %q: %s", bad, b)
		}
	}
	// Only these fields exist on the type, so a new field is a deliberate change.
	var names []string
	rt := reflect.TypeOf(u)
	for i := range rt.NumField() {
		names = append(names, rt.Field(i).Name)
	}
	if !reflect.DeepEqual(names, []string{"ID", "Username", "DisplayName", "Email", "Scopes", "Disabled"}) {
		t.Fatalf("PublicUser fields changed: %v", names)
	}
}

func TestUserLookupIsSafeConcurrently(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	id := userID(t, a, owner, "owner")
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				u, err := a.svc.User(context.Background(), id)
				if err != nil || u.Username != "owner" {
					t.Errorf("got %+v, %v", u, err)
					return
				}
				u.Scopes = append(u.Scopes, "x")
			}
		}()
	}
	wg.Wait()
}
