package ui

import (
	"strings"
	"testing"
)

func userMenu(items string) string {
	return `{"schema_version":0,"module":"identity","contributions":[` + items + `]}`
}

const goodUserItem = `{"kind":"menu.item","key":"signout","label":"Sign out","region":"user.menu","group":"session","order":900,"action":{"call":"identity.signOut"}}`

func TestUserMenuItemValidates(t *testing.T) {
	for _, item := range []string{
		goodUserItem,
		`{"kind":"menu.item","key":"keys","label":"Keys","region":"user.menu","group":"account","requires":"{app}.admin","action":{"navigate":"/access-keys"}}`,
		`{"kind":"menu.item","key":"x","label":"X","region":"user.menu","requires":"*","action":{"openRegion":"chat.drawer"}}`,
		`{"kind":"menu.item","key":"x","label":"X","region":"menu:plugin","group":"g","requires":"app.read","action":{"call":"openOverlay"}}`,
	} {
		c, err := Parse([]byte(userMenu(item)))
		if err != nil {
			t.Fatal(err)
		}
		if errs := Validate(c, Default); len(errs) != 0 {
			t.Errorf("%s: %v", item, errs)
		}
	}
	// A host that lists its regions still accepts user.menu: it is core.
	c, _ := Parse([]byte(userMenu(goodUserItem)))
	v := Default
	v.Regions = []string{"chat.drawer"}
	if errs := Validate(c, v); len(errs) != 0 {
		t.Errorf("user.menu with a host region list: %v", errs)
	}
}

func TestUserMenuRulesFire(t *testing.T) {
	for _, c := range []struct{ name, item, want string }{
		{"command", `{"kind":"command","key":"c","label":"C","region":"user.menu","action":{"call":"a.b"}}`, "takes only menu.item"},
		{"nav.item", `{"kind":"nav.item","key":"n","label":"N","region":"user.menu","route":"/x"}`, "takes only menu.item"},
		{"slot", `{"kind":"slot","key":"s","region":"user.menu"}`, "takes only menu.item"},
		{"dock.tab", `{"kind":"dock.tab","key":"t","region":"user.menu","view":"v"}`, "takes only menu.item"},
		{"when", strings.Replace(goodUserItem, `"order":900`, `"order":900,"when":"plugin"`, 1), "when is not allowed in user.menu"},
		{"call without a module", strings.Replace(goodUserItem, "identity.signOut", "signOut", 1), "<module>.<verb>"},
		{"call with an upper-case verb", strings.Replace(goodUserItem, "identity.signOut", "identity.SignOut", 1), "<module>.<verb>"},
		{"call with a snake-case verb", strings.Replace(goodUserItem, "identity.signOut", "identity.sign_out", 1), "<module>.<verb>"},
		{"requires an expression", strings.Replace(goodUserItem, `"order":900`, `"order":900,"requires":"a || b"`, 1), "not an expression"},
		{"requires with a stray brace", strings.Replace(goodUserItem, `"order":900`, `"order":900,"requires":"{x}.admin"`, 1), "not an expression"},
		{"no action", `{"kind":"menu.item","key":"n","label":"N","region":"user.menu"}`, "needs an action"},
		{"no label", strings.Replace(goodUserItem, `"label":"Sign out",`, "", 1), "needs a label"},
		{"requires on a command", `{"kind":"command","key":"c","label":"C","requires":"a","action":{"call":"a.b"}}`, `field "requires" does not apply`},
		{"group on a nav.item", `{"kind":"nav.item","key":"n","label":"N","route":"/x","group":"g"}`, `field "group" does not apply`},
	} {
		t.Run(c.name, func(t *testing.T) {
			cc, err := Parse([]byte(userMenu(c.item)))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range Validate(cc, Default) {
				if strings.Contains(e.Error(), c.want) {
					return
				}
			}
			t.Fatalf("errors = %v, want one containing %q", Validate(cc, Default), c.want)
		})
	}
}

func TestUserMenuKeysAreUniqueAndVersionIsSet(t *testing.T) {
	c, _ := Parse([]byte(userMenu(goodUserItem + "," + goodUserItem)))
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate menu.item") {
		t.Fatalf("errors = %v", errs)
	}
	if VocabularyVersion != "0.4.1" {
		t.Errorf("VocabularyVersion = %q", VocabularyVersion)
	}
}
