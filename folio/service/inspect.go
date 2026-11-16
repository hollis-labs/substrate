package service

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/tools/folio/internal/manifest"
)

// InspectOptions parameterises Service.Inspect.
type InspectOptions struct {
	// RepositoryRoot authorizes reading tracked workflows outside the module.
	RepositoryRoot string
	// TargetDir is a project previously written by New; it must hold a
	// .folio.yaml.
	TargetDir string
}

// FileDriftStatus classifies one tracked file.
type FileDriftStatus string

// The drift statuses. "Local" compares the file on disk with the digest folio
// recorded at generation; "upstream" compares what the preset renders today,
// with the recorded inputs, with that same digest.
const (
	DriftUnchanged       FileDriftStatus = "unchanged"
	DriftPresetUpdated   FileDriftStatus = "preset_updated"
	DriftLocallyModified FileDriftStatus = "locally_modified"
	DriftConflict        FileDriftStatus = "conflict"
	DriftAddedUpstream   FileDriftStatus = "added_upstream"
	DriftRemovedUpstream FileDriftStatus = "removed_upstream"
	DriftMissingLocally  FileDriftStatus = "missing_locally"
)

// FileDrift is one row of an InspectResult.
type FileDrift struct {
	Path string
	// Preset is the layer that owns the file (the last layer to write it).
	Preset string
	Status FileDriftStatus
	// Unstable marks a file whose preset renders different bytes on every run
	// (a uuid or randAlphaNum in the template). Its upstream side cannot be
	// compared, so Status reflects local edits only.
	Unstable bool
}

// InspectResult is the drift report for one project.
type InspectResult struct {
	// RootPreset and RootVersion name the top-level preset recorded in
	// .folio.yaml (the version it was generated at).
	RootPreset  string
	RootVersion string
	Files       []FileDrift
	Warnings    []string
}

// Inspect reports, without writing anything, how each file recorded in a
// project's .folio.yaml differs from what its preset would render today.
//
// It re-renders the recorded root preset with the recorded inputs and compares
// digests: the on-disk file and the fresh render each against the digest taken
// at generation. That is all the manifest supports; it stores digests, not
// content, so there is no base to merge from and a file changed on both sides
// can only be reported as a conflict.
//
// The render replays the clock and folio version recorded at generation
// (manifest generated_at and generator). Without that, every preset whose
// LICENSE reads {{ .now.Year }} would report LICENSE as preset_updated once the
// calendar year turns, with no change to the preset. A preset that is
// genuinely non-deterministic (uuid, randAlphaNum) cannot be replayed; such
// files are found by rendering twice and flagged Unstable instead.
//
// Not replayed: .target, the absolute directory of the render. A template that
// prints it reports drift when the project has moved.
func (s *Service) Inspect(opts InspectOptions) (InspectResult, error) {
	d, err := s.drift(opts, nil)
	if err != nil {
		return InspectResult{}, err
	}
	return d.InspectResult, nil
}

// driftState is Inspect's report with what it was computed from, for Sync to act on.
type driftState struct {
	InspectResult
	manifest manifest.Manifest
	layers   []layer
	rendered map[string]renderedFile
	// realTarget is the project directory with symlinks resolved.
	realTarget string
}

// drift re-renders the recorded root preset (with extra inputs laid over the recorded
// ones) and classifies every tracked and newly rendered file. It writes nothing.
func (s *Service) drift(opts InspectOptions, extra map[string]any) (*driftState, error) {
	if opts.TargetDir == "" {
		return nil, newErr(ErrInputInvalid, "target directory is required", nil)
	}
	m, err := manifest.Read(opts.TargetDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newErr(ErrManifestNotFound,
				fmt.Sprintf("%s has no %s: not a folio project", opts.TargetDir, manifest.ManifestFilename), nil)
		}
		return nil, newErr(ErrManifestInvalid, "read "+manifest.ManifestFilename, err)
	}
	if len(m.Presets) == 0 {
		return nil, newErr(ErrManifestInvalid, manifest.ManifestFilename+" records no presets", nil)
	}
	// A breadcrumb that names a removed preset fails here, before anything is rendered: a removed preset is
	// never replaced by the current preset of the same name.
	for _, ref := range m.Presets {
		if removedPreset(ref.ID, ref.Version) {
			return nil, errPresetRemoved(ref.ID, ref.Version)
		}
	}
	root := m.Presets[len(m.Presets)-1]

	now := m.GeneratedAt
	if now.IsZero() {
		now = s.now()
	}
	folioVersion := s.folioVersion
	if v, ok := strings.CutPrefix(m.Generator, "folio/"); ok && v != "" {
		folioVersion = v
	}

	inputs := m.Inputs
	if len(extra) > 0 {
		inputs = map[string]any{}
		for k, v := range m.Inputs {
			inputs[k] = v
		}
		for k, v := range extra {
			inputs[k] = v
		}
	}
	loaded, callerCtx, warnings, err := s.prepareRenderAt(NewOptions{
		PresetID:  root.ID,
		TargetDir: opts.TargetDir,
		Inputs:    inputs,
	}, now, folioVersion)
	if err != nil {
		return nil, err
	}
	layers, composeWarnings, err := s.composedLayers(loaded, callerCtx)
	if err != nil {
		var se *Error
		if errors.As(err, &se) && (se.Code == ErrInputMissing || se.Code == ErrInputInvalid) {
			return nil, newErr(ErrDriftUnverifiable,
				fmt.Sprintf("cannot verify: the current preset %q no longer accepts the inputs recorded in %s", root.ID, manifest.ManifestFilename), err)
		}
		return nil, err
	}
	warnings = append(warnings, composeWarnings...)

	recorded := map[string]string{}
	for _, ref := range m.Presets {
		recorded[ref.ID] = ref.Version
	}
	for _, l := range layers {
		if was, ok := recorded[l.Preset.ID]; ok && was != l.Preset.Version {
			warnings = append(warnings, fmt.Sprintf("preset %q was generated at %s; this folio ships %s", l.Preset.ID, was, l.Preset.Version))
		}
	}

	rendered, _, err := s.renderAllLayers(layers)
	if err != nil {
		return nil, err
	}
	again, _, err := s.renderAllLayers(layers)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	files := make([]FileDrift, 0, len(m.Files)+len(rendered))
	// .folio.yaml lives in the project, so whoever wrote it chooses these paths.
	// Never read outside the project on its say-so: the drift status would
	// tell them whether a file elsewhere on this machine matches a digest of
	// their choosing.
	realTarget, err := filepath.EvalSymlinks(opts.TargetDir)
	if err != nil {
		return nil, newErr(ErrReadFailed, "resolve "+opts.TargetDir, err)
	}
	var repository *os.Root
	if opts.RepositoryRoot != "" {
		_, rootPath, location, err := repositoryOptions(NewOptions{RepositoryRoot: opts.RepositoryRoot, TargetDir: opts.TargetDir, Inputs: m.Inputs})
		if err != nil {
			return nil, err
		}
		if location != m.RepositoryLocation {
			return nil, fmt.Errorf("repository location does not match manifest")
		}
		repository, err = os.OpenRoot(rootPath)
		if err != nil {
			return nil, err
		}
		defer repository.Close()
	}
	for path, rec := range m.Files {
		if !fs.ValidPath(path) || path == "." {
			warnings = append(warnings, fmt.Sprintf("%s records %q, which is not a relative path inside the project; skipped", manifest.ManifestFilename, path))
			continue
		}
		seen[path] = true
		rf, stillRendered := rendered[path]
		if !stillRendered {
			files = append(files, FileDrift{Path: path, Preset: rec.Preset, Status: DriftRemovedUpstream})
			continue
		}
		var onDisk []byte
		var readErr error
		if rec.RepositoryPath != "" {
			name, _ := m.Inputs["repo_name"].(string)
			if repository == nil {
				warnings = append(warnings, fmt.Sprintf("%s is a repository workflow; pass --repository-root to inspect it", rec.RepositoryPath))
				continue
			}
			if expected := workflowDestination(path, name); expected == "" || expected != rec.RepositoryPath || !fs.ValidPath(rec.RepositoryPath) {
				return nil, fmt.Errorf("invalid repository workflow destination %q", rec.RepositoryPath)
			}
			onDisk, readErr = repository.ReadFile(rec.RepositoryPath)
		} else {
			full := filepath.Join(opts.TargetDir, filepath.FromSlash(path))
			if resolved, rerr := filepath.EvalSymlinks(full); rerr == nil && !within(realTarget, resolved) {
				warnings = append(warnings, fmt.Sprintf("%s is a symlink that leaves the project; skipped", path))
				continue
			}
			onDisk, readErr = os.ReadFile(full)
		}
		if errors.Is(readErr, fs.ErrNotExist) {
			files = append(files, FileDrift{Path: path, Preset: rec.Preset, Status: DriftMissingLocally})
			continue
		} else if readErr != nil {
			return nil, newErr(ErrReadFailed, "read "+path, readErr)
		}
		unstable := !bytes.Equal(rf.File.Content, again[path].File.Content)
		locallyChanged := manifest.Digest(onDisk) != rec.DigestAtGen
		upstreamChanged := !unstable && manifest.Digest(rf.File.Content) != rec.DigestAtGen
		status := DriftUnchanged
		switch {
		case locallyChanged && upstreamChanged:
			status = DriftConflict
		case upstreamChanged:
			status = DriftPresetUpdated
		case locallyChanged:
			status = DriftLocallyModified
		}
		if unstable {
			warnings = append(warnings, fmt.Sprintf("%s renders different bytes on every run (uuid or randAlphaNum); its preset side cannot be compared", path))
		}
		files = append(files, FileDrift{Path: path, Preset: rec.Preset, Status: status, Unstable: unstable})
	}
	for path, rf := range rendered {
		if !seen[path] {
			files = append(files, FileDrift{Path: path, Preset: rf.PresetID, Status: DriftAddedUpstream})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(warnings)

	return &driftState{
		InspectResult: InspectResult{RootPreset: root.ID, RootVersion: root.Version, Files: files, Warnings: warnings},
		manifest:      m,
		layers:        layers,
		rendered:      rendered,
		realTarget:    realTarget,
	}, nil
}

// within reports whether path is dir or lies beneath it. Both are already
// symlink-resolved.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
