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
