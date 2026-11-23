package service

import (
	"fmt"
	"strconv"
	"strings"
)

// lastReleaseWithRemoved is the last folio release whose bundled presets include the presets listed in
// removedPresets (and app-dashboard before 3.0.0). The tag is folio/v0.0.1.
const lastReleaseWithRemoved = "v0.0.1"

// removedPresets are bundled presets this folio no longer ships at all: a project whose .folio.yaml
// names one cannot be inspected here, and `folio new` of one fails with the same sentence. They were
// removed when presets began rendering Substrate apps (go-webui, Chimera and go-service-app are gone;
// chat-app waits for a pinned kit-chat).
var removedPresets = map[string]bool{
	"go-service-app":               true,
	"chat-app":                     true,
	"app-dashboard-chimera-plugin": true,
	"chat-app-chimera-plugin":      true,
	"chimera-plugin-fixture":       true,
}

// removedPreset reports whether the preset id and version recorded in a breadcrumb (or asked for on the
// command line, with an empty version) name a preset this folio no longer ships. app-dashboard and its
// old alias sysop-ui are now retired at every version. Pre-3.0 breadcrumbs retain
// their older recovery guidance; no project is re-rendered onto a replacement.
func removedPreset(id, _ string) bool {
	return retiredFrontendPreset(id) || removedPresets[id]
}

// retiredFrontendPreset includes the old dashboard alias. Retired IDs cannot be
// revived accidentally by a user preset directory or an external pack.
func retiredFrontendPreset(id string) bool {
	switch id {
	case "app-ade", "app-chat", "app-dashboard", "substrate-frontend", "sysop-ui":
		return true
	}
	return false
}

func errPresetRemoved(id, version string) *Error {
	what := fmt.Sprintf("preset %q", id)
	if version != "" {
		what = fmt.Sprintf("preset %q %s", id, version)
	}
	// Keep the original recovery guidance for pre-3 dashboard breadcrumbs.
	legacyDashboard := false
	if id == "app-dashboard" || id == "sysop-ui" {
		major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
		n, err := strconv.Atoi(major)
		legacyDashboard = err == nil && n < 3
	}
	if retiredFrontendPreset(id) && !legacyDashboard {
		return newErr(ErrPresetRemoved, fmt.Sprintf(
			"%s was retired with the legacy frontend scaffolds. Historical source is preserved at "+
				"https://github.com/hollis-labs/substrate/tree/2bf3f4951c219c6a921c26276f1dbe251d6efe48/folio. "+
				"Use the Surface preset pack for new UI apps; existing projects are not rewritten.", what), nil)
	}
	return newErr(ErrPresetRemoved, fmt.Sprintf(
		"%s was removed from folio: folio %s (tag folio/%s) is the last release that has it. "+
			"This folio does not re-render it onto another preset; use that release to inspect the project "+
			"(go install github.com/hollis-labs/tools/folio/cmd/folio@%s)",
		what, lastReleaseWithRemoved, lastReleaseWithRemoved, lastReleaseWithRemoved), nil)
}
