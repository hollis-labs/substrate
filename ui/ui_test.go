package ui

import (
	"strings"
	"testing"
)

const good = `{
  "schema_version": 0, "module": "demo",
  "package": {"name": "@hollis-labs/kit-demo", "version_ref": "kits.kit-demo"},
  "nav": [{"kind":"nav.group","key":"access","label":"Access"},
          {"kind":"nav.item","key":"keys","label":"Keys","group":"access","route":"/keys","scope":"{app}.admin"}],
  "settings": [{"id":"demo","scope_kind":"app"}],
  "panels": [{"kind":"panel","key":"caller","region":"shell.header","view":"me"}],
  "widgets": [{"kind":"widget","key":"health","view":"health"}],
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
		{"version literal", `"kits.kit-demo"`, `"1.2.3"`, "versions live in the BOM only"},
		{"unscoped route", `"scope":"authenticated"`, `"scope":""`, "scope is required"},
		{"route outside api", `/api/demo`, `/other/demo`, "must be under /api/ or /admin/"},
		{"bad method", `"GET"`, `"TRACE"`, "method must be"},
		{"unknown nav kind", `"nav.group"`, `"nav.tree"`, "not in the vocabulary"},
		{"item without route", `"route":"/keys",`, ``, "needs a route"},
		{"bad settings scope", `"scope_kind":"app"`, `"scope_kind":"galaxy"`, "scope_kind"},
		{"wrong version", `"schema_version": 0`, `"schema_version": 3`, "schema_version 3"},
		{"panel without view", `"view":"me"`, `"view":""`, "needs a view"},
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
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), c.want) {
				t.Fatalf("errors = %v, want one containing %q", errs, c.want)
			}
		})
	}
}

func TestRegionsAreCheckedOnlyWhenTheVocabularyListsThem(t *testing.T) {
	c, _ := Parse([]byte(good))
	if errs := Validate(c, Default); len(errs) != 0 {
		t.Fatalf("empty region list admits any region: %v", errs)
	}
	v := Default
	v.Regions = []string{"dashboard"}
	if errs := Validate(c, v); len(errs) != 1 || !strings.Contains(errs[0].Error(), "shell.header") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestDuplicateKeysAndRoutes(t *testing.T) {
	dup := strings.Replace(good, `"widgets": [{"kind":"widget","key":"health","view":"health"}]`,
		`"widgets": [{"kind":"widget","key":"health","view":"a"},{"kind":"widget","key":"health","view":"b"}]`, 1)
	c, _ := Parse([]byte(dup))
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate widgets key") {
		t.Fatalf("errors = %v", errs)
	}
	c, _ = Parse([]byte(good))
	c.Routes = append(c.Routes, c.Routes[0])
	if errs := Validate(c, Default); len(errs) != 1 || !strings.Contains(errs[0].Error(), "duplicate route") {
		t.Fatalf("errors = %v", errs)
	}
}
