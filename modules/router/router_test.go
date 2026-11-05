package router_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	librouter "github.com/hollis-labs/libs/core/util/router"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/router"
)

const rules = `{"version":"t.1","strategy":"first_match","rules":[
  {"id":"save-note","priority":10,"when":[{"field":"kind","op":"eq","value":"note"}],"target":"notes.write","auto":true}]}`

func ruleFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRulesFromAFileRouteASubject(t *testing.T) {
	router.Reset()
	t.Cleanup(router.Reset)
	run, err := boot.Start(context.Background(), "t", []string{"router"}, `{"router":{"rules_file":"`+ruleFile(t, rules)+`"}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	d, err := router.Route(context.Background(), run.App, librouter.Subject{ID: "m1", Fields: map[string]string{"kind": "note"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Matches) != 1 || d.Matches[0].Target != "notes.write" {
		t.Fatalf("decision: %+v", d)
	}
	miss, _ := router.Route(context.Background(), run.App, librouter.Subject{ID: "m2", Fields: map[string]string{"kind": "other"}})
	if len(miss.Matches) != 0 {
		t.Errorf("a non-matching subject matched: %+v", miss)
	}
}

func TestRulesInCodeWork(t *testing.T) {
	router.Reset()
	t.Cleanup(router.Reset)
	set, err := librouter.ParseRuleSet([]byte(rules))
	if err != nil {
		t.Fatal(err)
	}
	router.UseRules(set)
	run, err := boot.Start(context.Background(), "t", []string{"router"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	if router.From(run.App) == nil {
		t.Fatal("router not published")
	}
}

func TestBadRuleSourcesFailStartWithoutLeakingThePath(t *testing.T) {
	router.Reset()
	t.Cleanup(router.Reset)
	set, _ := librouter.ParseRuleSet([]byte(rules))
	for name, c := range map[string]struct {
		cfg, want string
		code      bool
	}{
		"no rules":         {`{}`, "needs rules_file", false},
		"both sources":     {`{"router":{"rules_file":"` + ruleFile(t, rules) + `"}}`, "not both", true},
		"unreadable file":  {`{"router":{"rules_file":"/nonexistent/secret-dir/rules.json"}}`, "cannot read", false},
		"unparseable":      {`{"router":{"rules_file":"` + ruleFile(t, `{"version":`) + `"}}`, "rules_file", false},
		"fails to compile": {`{"router":{"rules_file":"` + ruleFile(t, `{"version":"","strategy":"first_match","rules":[]}`) + `"}}`, "router:", false},
	} {
		router.Reset()
		if c.code {
			router.UseRules(set)
		}
		_, err := boot.Start(context.Background(), "t", []string{"router"}, c.cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
			continue
		}
		if strings.Contains(err.Error(), "secret-dir") {
			t.Errorf("%s: the error leaks the path: %v", name, err)
		}
	}
}
