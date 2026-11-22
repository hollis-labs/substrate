package folio_test

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/service"
)

func splitCatalog(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	generic, surface := fstest.MapFS{}, fstest.MapFS{}
	err := fs.WalkDir(folio.BundledPresets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(folio.BundledPresets, name)
		if err != nil {
			return err
		}
		dest := generic
		if strings.HasPrefix(name, "presets/surface-") {
			dest = surface
		}
		dest[name] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return generic, surface
}

// Moving Surface templates to their owner must preserve exact generated output,
// including its composition of Substrate's agent-dir layer and file provenance.
func TestPresetPacksSplitPreservesSurfaceRendering(t *testing.T) {
	generic, surface := splitCatalog(t)
	combined, err := folio.WithPresetPacks(generic, surface)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for name := range generic {
		files = append(files, name)
	}
	for name := range surface {
		files = append(files, name)
	}
	if fsErr := fstest.TestFS(combined, files...); fsErr != nil {
		t.Fatal(fsErr)
	}
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	makeService := func(catalog fs.FS) *service.Service {
		return service.New(service.Options{BundledFS: catalog, UserDir: t.TempDir(), FolioVersion: folio.Version, Now: now})
	}
	for _, id := range []string{"surface-app", "surface-desk"} {
		opts := service.NewOptions{PresetID: id, TargetDir: "example", Inputs: map[string]any{"repo_name": "pack-check"}}
		before, beforeErr := makeService(folio.BundledPresets).Plan(opts)
		if beforeErr != nil {
			t.Fatal(beforeErr)
		}
		after, err := makeService(combined).Plan(opts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s output changed across pack split", id)
		}
	}
	original, _, err := makeService(folio.BundledPresets).ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	split, _, err := makeService(combined).ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, split) {
		t.Fatal("split changed preset discovery")
	}
	// A Surface-only catalog cannot quietly omit the shared agent layer.
	isolated := makeService(surface)
	if _, err := isolated.Plan(service.NewOptions{PresetID: "surface-desk", Inputs: map[string]any{"repo_name": "pack-check"}}); err == nil {
		t.Fatal("missing cross-owner agent-dir dependency was accepted")
	}
	// Version constraints still apply across owners.
	entry := generic["presets/agent-dir/preset.yaml"]
	entry.Data = []byte(strings.Replace(string(entry.Data), "version: 0.1.0", "version: 9.0.0", 1))
	if _, err := makeService(combined).Plan(service.NewOptions{PresetID: "surface-desk", Inputs: map[string]any{"repo_name": "pack-check"}}); err == nil {
		t.Fatal("incompatible cross-owner agent-dir version was accepted")
	}
}

func TestPresetPacksRejectAmbiguousOrMissingCatalog(t *testing.T) {
	generic, surface := splitCatalog(t)
	if _, err := folio.WithPresetPacks(generic, generic); err == nil {
		t.Fatal("duplicate preset ID accepted")
	}
	if _, err := folio.WithPresetPacks(generic, fstest.MapFS{}); err == nil {
		t.Fatal("missing pack root accepted")
	}
	combined, err := folio.WithPresetPacks(generic, surface)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := combined.Open("presets/../go-lib/preset.yaml"); err == nil {
		t.Fatal("invalid path accepted")
	}
}
