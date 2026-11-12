// Package scripts holds the one copy of scan-public that Substrate ships: the
// Python script itself, embedded so `substrate scan-public` runs exactly the
// file in this directory (there is no second copy to drift).
package scripts

import _ "embed"

// ScanPublic is the source of scripts/scan-public.
//
//go:embed scan-public
var ScanPublic []byte
