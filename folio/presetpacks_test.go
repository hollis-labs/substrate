package folio_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/substrate/folio"
)

func TestPresetPacksFilesystem(t *testing.T) {
	base := fstest.MapFS{"presets/base/preset.yaml": {Data: []byte("base")}}
	pack := fstest.MapFS{"presets/addon/preset.yaml": {Data: []byte("addon")}}
	combined, err := folio.WithPresetPacks(base, pack)
	if err != nil {
		t.Fatal(err)
	}
	if testErr := fstest.TestFS(combined, "presets/base/preset.yaml", "presets/addon/preset.yaml"); testErr != nil {
		t.Fatal(testErr)
	}
	if _, openErr := combined.Open("presets/../base/preset.yaml"); openErr == nil {
		t.Fatal("invalid path accepted")
	}
	if _, dupErr := folio.WithPresetPacks(base, base); dupErr == nil {
		t.Fatal("duplicate preset directory accepted")
	}
	if _, missingErr := folio.WithPresetPacks(base, fstest.MapFS{}); missingErr == nil {
		t.Fatal("missing catalog accepted")
	}
	if _, nilErr := folio.WithPresetPacks(base, nil); nilErr == nil {
		t.Fatal("nil pack accepted")
	}
	only, onlyErr := folio.WithPresetPacks(nil, pack)
	if onlyErr != nil {
		t.Fatal(onlyErr)
	}
	if testErr := fstest.TestFS(only, "presets/addon/preset.yaml"); testErr != nil {
		t.Fatal(testErr)
	}
	if _, statErr := fs.Stat(combined, "presets/addon"); statErr != nil {
		t.Fatal(statErr)
	}
}
