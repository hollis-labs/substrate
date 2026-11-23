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
// old alias sysop-ui are removed below 3.0.0: 3.0.0 is a different preset (a Substrate app) with other
// inputs, so a 2.x breadcrumb is never re-rendered onto it.
func removedPreset(id, version string) bool {
	if removedPresets[id] {
		return true
	}
	if (id == "app-dashboard" || id == "sysop-ui") && version != "" {
		major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
		if n, err := strconv.Atoi(major); err == nil && n < 3 {
			return true
		}
	}
	return false
}

// errPresetRemoved is the error for a removed preset: it names the preset, the version when known and the
// folio release that last had it, never a stack trace and never a silent re-render onto another preset.
func errPresetRemoved(id, version string) *Error {
	what := fmt.Sprintf("preset %q", id)
	if version != "" {
		what = fmt.Sprintf("preset %q %s", id, version)
	}
	return newErr(ErrPresetRemoved, fmt.Sprintf(
		"%s was removed from folio: folio %s (tag folio/%s) is the last release that has it. "+
			"This folio does not re-render it onto another preset; use that release to inspect the project "+
			"(go install github.com/hollis-labs/tools/folio/cmd/folio@%s)",
		what, lastReleaseWithRemoved, lastReleaseWithRemoved, lastReleaseWithRemoved), nil)
}
