// Package catalog is the machine-readable list of modules, profiles and
// variants the builder validates manifests against.
package catalog

// Module describes one catalogue entry.
type Module struct {
	Name     string
	Import   string   // Go import path whose init() registers the module
	Requires []string // other modules that must be selected
	Status   string   // "ready" or "planned"
}

var Modules = []Module{
	{Name: "config", Import: "github.com/hollis-labs/substrate/modules/config", Status: "ready"},
	{Name: "logging", Import: "github.com/hollis-labs/substrate/modules/logging", Status: "ready"},
	{Name: "health", Import: "github.com/hollis-labs/substrate/modules/health", Status: "ready"},
	{Name: "httphost", Import: "github.com/hollis-labs/substrate/modules/httphost", Requires: []string{"health"}, Status: "ready"},
	{Name: "capabilities", Import: "github.com/hollis-labs/substrate/modules/capabilities", Requires: []string{"health"}, Status: "ready"},
	// Step 3 of the build order; selectable in the catalogue, not yet buildable.
	{Name: "mcp", Import: "github.com/hollis-labs/substrate/modules/mcp", Requires: []string{"httphost"}, Status: "ready"},
	{Name: "policy", Import: "github.com/hollis-labs/substrate/modules/policy", Status: "ready"},
	{Name: "auth", Import: "github.com/hollis-labs/substrate/modules/auth", Status: "ready"},
	{Name: "secrets", Import: "github.com/hollis-labs/substrate/modules/secrets", Requires: []string{"logging"}, Status: "ready"},
	{Name: "database", Import: "github.com/hollis-labs/substrate/modules/database", Requires: []string{"health"}, Status: "ready"},
	{Name: "pluginhost", Import: "github.com/hollis-labs/substrate/modules/pluginhost", Requires: []string{"health"}, Status: "ready"},
	{Name: "events", Import: "github.com/hollis-labs/substrate/modules/events", Requires: []string{"httphost"}, Status: "ready"},
	{Name: "queue", Import: "github.com/hollis-labs/substrate/modules/queue", Requires: []string{"database"}, Status: "ready"},
	{Name: "scheduler", Import: "github.com/hollis-labs/substrate/modules/scheduler", Requires: []string{"database"}, Status: "ready"},
	{Name: "router", Import: "github.com/hollis-labs/substrate/modules/router", Status: "ready"},
	{Name: "cli", Import: "github.com/hollis-labs/substrate/modules/cli", Requires: []string{"health"}, Status: "ready"},
}

// Profiles are composable module bundles. Names and boundaries are a starting
// point pending Chrispian's decision.
var Profiles = map[string][]string{
	"minimal": {"config", "logging", "health"},
	// agentic: minimal plus the capability report, secrets, identity and MCP.
	// policy, database, queue, scheduler, router and pluginhost are not in it:
	// policy needs the app's PDP, router needs the app's rules, and the rest own app
	// data, jobs and plugins. Apps add them under
	// modules.add. mcp pulls in httphost, so agentic needs an HTTP-capable binary.
	"agentic": {"config", "logging", "health", "capabilities", "secrets", "auth", "mcp"},
}

// Variants say what the binary is.
var Variants = map[string]struct{ Needs []string }{
	"service":      {},
	"headless":     {},
	"app-host":     {Needs: []string{"httphost"}},
	"cli":          {Needs: []string{"cli"}},
	"microservice": {Needs: []string{"httphost"}},
}

func Find(name string) (Module, bool) {
	for _, m := range Modules {
		if m.Name == name {
			return m, true
		}
	}
	return Module{}, false
}
