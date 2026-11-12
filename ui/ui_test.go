package ui

import (
	"strings"
	"testing"
)

const good = `{
  "schema_version": 0, "module": "demo",
  "package": {"name": "@hollis-labs/kit-demo"},
  "contributions": [
    {"kind":"nav.group","key":"access","label":"Access","collapsible":true},
    {"kind":"nav.item","key":"keys","label":"Keys","region":"nav.group:access","route":"/keys","requires":"{app}.admin","badge":"key-count"},
    {"kind":"settings.region","key":"demo-settings","label":"Demo","section_id":"demo","groups":[{"id":"demo","scope_kind":"app"}]},
    {"kind":"surface","key":"keys-page","route":"/keys","layout":"list","title":"API keys"},
    {"kind":"panel","key":"caller","label":"Caller","view":"me"},
    {"kind":"dock.tab","key":"chat","label":"Chat","region":"chat.drawer","view":"chat","shortcut":"mod+k","placements":["dock","aside"]},
    {"kind":"widget","key":"health","view":"health"},
    {"kind":"slot","key":"chat-trigger","region":"chat.composer","opens":"chat"}
  ],
  "routes": [{"method":"GET","path":"/api/demo","scope":"authenticated"}]
}`

func TestGoodContributionValidates(t *testing.T) {
	c, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(c, Default); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
}

func TestStrictParseRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte(`{"schema_version":0,"module":"x","surprise":1}`)); err == nil {
		t.Fatal("unknown field must be an error")
	}
}

func TestEachRuleFires(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"unscoped package", `"@hollis-labs/kit-demo"`, `"kit-demo"`, "must be a scoped npm name"},
		{"unscoped route", `"scope":"authenticated"`, `"scope":""`, "scope is required"},
		{"route outside api", `/api/demo`, `/other/demo`, "must be under /api/ or /admin/"},
		{"bad method", `"GET"`, `"TRACE"`, "method must be"},
		{"old kind name", `"dock.tab"`, `"drawer.tab"`, "not in the vocabulary"},
		{"unknown kind", `"widget"`, `"gadget"`, "not in the vocabulary"},
		{"nav item without route", `"route":"/keys","requires"`, `"requires"`, "must be an absolute frontend path"},
		{"frontend route is an api path", `"route":"/keys","requires"`, `"route":"/api/keys","requires"`, "backend path"},
		{"wrong schema", `"schema_version": 0`, `"schema_version": 3`, "schema_version 3"},
		{"panel without view", `"view":"me"`, `"view":""`, "needs a view"},
		{"bad scope kind", `"scope_kind":"app"`, `"scope_kind":"galaxy"`, "scope_kind"},
		{"settings region without groups", `"groups":[{"id":"demo","scope_kind":"app"}]`, `"groups":[]`, "at least one group"},
		{"bad surface layout", `"layout":"list"`, `"layout":"carousel"`, "layout"},
		{"surface without title", `"title":"API keys"`, `"title":""`, "needs a title"},
		{"nav item in a surface region", `"region":"nav.group:access"`, `"region":"surface.page"`, "not allowed for this kind"},
		{"dock tab with no region", `"region":"chat.drawer",`, ``, "region is required"},
		{"field on the wrong kind", `"kind":"widget","key":"health"`, `"kind":"widget","key":"health","route":"/x"`, `field "route" does not apply`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(good, c.from) {
				t.Fatalf("fixture lacks %q", c.from)
			}
			cc, err := Parse([]byte(strings.Replace(good, c.from, c.to, 1)))
			if err != nil {
				t.Fatal(err)
			}
			errs := Validate(cc, Default)
			found := false
			for _, e := range errs {
				found = found || strings.Contains(e.Error(), c.want)
			}
			if !found {
				t.Fatalf("errors = %v, want one containing %q", errs, c.want)
			}
		})
	}
}

func TestRegionsAreCheckedOnlyWhenTheHostPublishesThem(t *testing.T) {
	c, _ := Parse([]byte(good))
	if errs := Validate(c, Default); len(errs) != 0 {
		t.Fatalf("an empty region list admits host-defined regions: %v", errs)
	}
	v := Default
	v.Regions = []string{"chat.drawer"}
	errs := Validate(c, v)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "chat.composer") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestDuplicateKeysAndRoutes(t *testing.T) {
	c, _ := Parse([]byte(good))
	c.Contributions = append(c.Contributions, c.Contributions[len(c.Contributions)-2])
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate widget") {
		t.Fatalf("errors = %v", errs)
	}
	c, _ = Parse([]byte(good))
	c.Routes = append(c.Routes, c.Routes[0])
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate route") {
		t.Fatalf("errors = %v", errs)
	}
}

// v0.4: menu.item and command.
const menus = `{
  "schema_version": 0, "module": "demo",
  "envelopes": {"emits": ["demo.card@1", "demo.list@2"]},
  "contributions": [
    {"kind":"surface","key":"plugins","route":"/plugins","layout":"list","title":"Plugins"},
    {"kind":"menu.item","key":"stop","label":"Stop","icon":"stop","region":"menu:plugin","order":10,"when":"plugin","risk":"medium","shortcut":"mod+.","action":{"call":"plugins.stop","input":{"force":false}}},
    {"kind":"menu.item","key":"remove","label":"Remove","region":"menu:plugin","risk":"high","action":{"call":"plugins.remove"}},
    {"kind":"menu.item","key":"add","label":"Add plugin","region":"toolbar.primary","action":{"navigate":"/plugins"}},
    {"kind":"menu.item","key":"quick","label":"Stop","region":"row.actions","action":{"openRegion":"chat.drawer","tab":"chat"}},
    {"kind":"menu.item","key":"host","label":"Host menu","region":"chat.toolbar","action":{"call":"openOverlay"}},
    {"kind":"command","key":"go-plugins","label":"Go to plugins","keywords":["extensions","addons"],"group":"Navigate","action":{"navigate":"/plugins"}}
  ]
}`

func TestV04ContributionValidates(t *testing.T) {
	c, err := Parse([]byte(menus))
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(c, Default); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := c.Contributions[len(c.Contributions)-1].EffectiveRegion(); got != RegionCommandPalette {
		t.Errorf("a command with no region is in %q, want %q", got, RegionCommandPalette)
	}
}

func TestV04RulesFire(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"menu.item without a label", `"label":"Add plugin",`, ``, "needs a label"},
		{"menu.item without an action", `,"action":{"navigate":"/plugins"}},
    {"kind":"menu.item","key":"quick"`, `},
    {"kind":"menu.item","key":"quick"`, "needs an action"},
		{"two actions", `{"call":"plugins.remove"}`, `{"call":"plugins.remove","navigate":"/plugins"}`, "exactly one of"},
		{"empty action", `{"call":"plugins.remove"}`, `{}`, "exactly one of"},
		{"navigate to a url", `"navigate":"/plugins"}},
    {"kind":"menu.item","key":"quick"`, `"navigate":"https://example.com/x"}},
    {"kind":"menu.item","key":"quick"`, "absolute frontend path"},
		{"navigate to an api path", `{"kind":"command","key":"go-plugins","label":"Go to plugins","keywords":["extensions","addons"],"group":"Navigate","action":{"navigate":"/plugins"}}`, `{"kind":"command","key":"go-plugins","label":"Go","action":{"navigate":"/api/plugins"}}`, "backend path"},
		{"openRegion with input", `{"openRegion":"chat.drawer","tab":"chat"}`, `{"openRegion":"chat.drawer","input":{"a":1}}`, "takes no input"},
		{"call with a tab", `{"call":"plugins.remove"}`, `{"call":"plugins.remove","tab":"x"}`, "takes no tab"},
		{"unknown risk", `"risk":"high"`, `"risk":"extreme"`, "must be low, medium or high"},
		{"negative order", `"order":10`, `"order":-1`, "must not be negative"},
		{"when outside a menu region", `"region":"toolbar.primary"`, `"region":"toolbar.primary","when":"plugin"`, "applies only to a menu:<subject-kind> region"},
		{"menu.item in the palette", `"key":"add","label":"Add plugin","region":"toolbar.primary"`, `"key":"add","label":"Add plugin","region":"command.palette"`, "not allowed for this kind"},
		{"command in a menu", `{"kind":"command","key":"go-plugins"`, `{"kind":"command","region":"menu:plugin","key":"go-plugins"`, "not allowed for this kind"},
		{"command takes no order", `"keywords":["extensions","addons"]`, `"keywords":["extensions","addons"],"order":5`, `field "order" does not apply`},
		{"menu.item takes no keywords", `"risk":"high",`, `"risk":"high","keywords":["x"],`, `field "keywords" does not apply`},
		{"bad emitted envelope", `"demo.card@1"`, `"demo.card"`, "must be kind@version"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(menus, c.from) {
				t.Fatalf("fixture lacks %q", c.from)
			}
			cc, err := Parse([]byte(strings.Replace(menus, c.from, c.to, 1)))
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

// The same menu.item key may be used in two regions, but not twice in one.
func TestMenuItemKeysAreUniquePerRegion(t *testing.T) {
	c, _ := Parse([]byte(menus))
	dup := c.Contributions[1]
	dup.Region = "menu:session"
	c.Contributions = append(c.Contributions, dup)
	if errs := Validate(c, Default); len(errs) != 0 {
		t.Fatalf("the same key in another region: %v", errs)
	}
	c.Contributions = append(c.Contributions, c.Contributions[1])
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate menu.item") {
		t.Fatalf("the same key twice in one region: %v", errs)
	}
}

func TestHostRegionListGovernsMenuItemsToo(t *testing.T) {
	c, _ := Parse([]byte(menus))
	v := Default
	v.Regions = []string{"chat.drawer"}
	errs := Validate(c, v)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "chat.toolbar") {
		t.Fatalf("errors = %v", errs)
	}
}
