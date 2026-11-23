package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/tools/folio/internal/manifest"
	"github.com/hollis-labs/tools/folio/internal/preset"
)

// SyncOptions parameterises Service.Sync.
type SyncOptions struct {
	// TargetDir is a project previously written by New; it must hold a .folio.yaml.
	TargetDir string
	// Plan reports what Sync would do and writes nothing.
	Plan bool
	// Inputs are laid over the inputs recorded in .folio.yaml: a value a newer preset
	// version requires, or a changed one. They are recorded when Sync writes.
	Inputs map[string]any
	// TheirsTo, when set, is a suffix: for a file in conflict, the preset's new rendering
	// is written beside it as <path><suffix> so the person can merge it by hand.
	TheirsTo string
}

// SyncAction is what Sync did (or would do) with one file.
type SyncAction string

// The sync actions.
const (
	// SyncUnchanged: nothing to do.
	SyncUnchanged SyncAction = "unchanged"
	// SyncUpdated: the preset's new rendering was written (the file was as generated, or
	// it is new upstream).
	SyncUpdated SyncAction = "updated"
	// SyncAdded: a file the preset now renders was written.
	SyncAdded SyncAction = "added"
	// SyncOverwritten: the file had local edits, and a sync rule with policy overwrite
	// says the preset's copy is canonical, so it was replaced.
	SyncOverwritten SyncAction = "overwritten"
	// SyncKeptLocal: the file was edited and the preset has nothing new for it.
	SyncKeptLocal SyncAction = "kept_local"
	// SyncSkipped: a sync rule with policy skip covers the file, so it is never written.
	SyncSkipped SyncAction = "skipped"
	// SyncConflict: both the file and the preset changed; the file was left alone.
	SyncConflict SyncAction = "conflict"
	// SyncRemovedUpstream: the preset no longer renders the file; it was left alone.
	SyncRemovedUpstream SyncAction = "removed_upstream"
	// SyncMissingLocally: the file was deleted from the project; it was not restored.
	SyncMissingLocally SyncAction = "missing_locally"
)

// SyncFile is one row of a SyncResult.
type SyncFile struct {
	Path   string
	Preset string
	// Status is what Inspect reports for the file.
	Status FileDriftStatus
	Action SyncAction
	// Policy is the sync policy that applied ("" when no rule or default matched).
	Policy string
	// Theirs is where the preset's new rendering was written beside a conflict.
	Theirs string
	Detail string
}

// SyncResult is what Sync did (or, with Plan, would do).
type SyncResult struct {
	RootPreset  string
	FromVersion string // the root preset's version recorded before the sync
	ToVersion   string // the root preset's version the project was synced to
	Plan        bool
	Files       []SyncFile
	Warnings    []string
}

// Conflicts are the files left alone because both sides changed.
func (r SyncResult) Conflicts() []SyncFile {
	var out []SyncFile
	for _, f := range r.Files {
		if f.Action == SyncConflict {
			out = append(out, f)
		}
	}
	return out
}

// Sync brings a project up to its preset as it renders today, without overwriting what
// the person wrote. It acts on Inspect's classification:
//
//   - preset_updated, added_upstream: the new rendering is written and recorded.
//   - locally_modified: left alone.
//   - conflict: left alone and reported (TheirsTo writes the new rendering beside it),
//     unless a sync rule with policy overwrite covers the file.
//   - removed_upstream, missing_locally: left alone and reported.
//
// A file covered by a sync rule with policy skip is never written, whatever its state:
// that is how a preset marks files the person authors. Rules are tried in order, the
// root preset's first, then those of the layer that renders the file; the first rule
// whose glob matches decides ("**" matches any number of path segments). Without a
// match, the layer's sync.default applies; prompt and three-way both mean "never
// overwrite local edits" here, since there is no merge in this version.
//
// A file the preset newly renders where the project already has an untracked file is
// treated as a conflict, never overwritten. Repository workflows (files placed outside
// the module) are not inspected without a repository root, so they are not synced.
//
// Without Plan, .folio.yaml is rewritten: the presets' versions, the inputs, each
// written file's digest, and a sync_history entry.
func (s *Service) Sync(opts SyncOptions) (SyncResult, error) {
	d, err := s.drift(InspectOptions{TargetDir: opts.TargetDir}, opts.Inputs)
	if err != nil {
		var se *Error
		if errors.As(err, &se) && se.Code == ErrDriftUnverifiable {
			return SyncResult{}, newErr(ErrInputMissing, se.Message+"; pass the inputs it now needs with --input key=value", se.Err)
		}
		return SyncResult{}, err
	}
	if opts.TheirsTo != "" && (strings.ContainsAny(opts.TheirsTo, `/\`) || strings.TrimLeft(opts.TheirsTo, ".") == "") {
		return SyncResult{}, newErr(ErrInputInvalid, fmt.Sprintf("--theirs-to %q must be a file-name suffix such as .folio-new", opts.TheirsTo), nil)
	}
	m := d.manifest
	root := d.layers[len(d.layers)-1]
	res := SyncResult{
		RootPreset:  d.RootPreset,
		FromVersion: d.RootVersion,
		ToVersion:   root.Preset.Version,
		Plan:        opts.Plan,
		Warnings:    d.Warnings,
	}
	byID := map[string]*preset.Preset{}
	for _, l := range d.layers {
		byID[l.Preset.ID] = l.Preset
	}
	files := map[string]manifest.FileRecord{}
	for k, v := range m.Files {
		files[k] = v
	}

	// A write that fails stops the sync, but .folio.yaml is still brought up to date
	// for the files already written, so the next run does not take them for local edits.
	var failed error
each:
	for _, f := range d.Files {
		row := SyncFile{Path: f.Path, Preset: f.Preset, Status: f.Status}
		row.Policy = syncPolicy(f.Path, root.Preset, byID[f.Preset])
		rf, rendered := d.rendered[f.Path]
		write := func(action SyncAction) error {
			row.Action = action
			if opts.Plan {
				return nil
			}
			if err := s.writeProjectFile(d.realTarget, f.Path, rf); err != nil {
				return err
			}
			files[f.Path] = manifest.FileRecord{Preset: rf.PresetID, DigestAtGen: manifest.Digest(rf.File.Content)}
			return nil
		}
		switch {
		case f.Status == DriftUnchanged:
			row.Action = SyncUnchanged
		case row.Policy == "skip":
			// Authored: never written, whatever has happened to it or to the preset.
			row.Action, row.Detail = SyncSkipped, "a skip rule covers it"
		case f.Status == DriftPresetUpdated:
			if err := write(SyncUpdated); err != nil {
				failed = err
				break each
			}
		case f.Status == DriftAddedUpstream:
			existing, err := s.readProjectFile(d.realTarget, f.Path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				if werr := write(SyncAdded); werr != nil {
					failed = werr
					break each
				}
			case err != nil:
				failed = err
				break each
			case manifest.Digest(existing) == manifest.Digest(rf.File.Content):
				// Already there as the preset renders it: only recorded.
				row.Action, row.Detail = SyncAdded, "already present; recorded"
				if !opts.Plan {
					files[f.Path] = manifest.FileRecord{Preset: rf.PresetID, DigestAtGen: manifest.Digest(rf.File.Content)}
				}
			default:
				row.Action, row.Detail = SyncConflict, "the preset now renders this path and the project has its own file there"
				if err := s.theirs(&row, opts, d.realTarget, rf); err != nil {
					failed = err
					break each
				}
			}
		case f.Status == DriftConflict && row.Policy == "overwrite" && rendered:
			row.Detail = "an overwrite rule covers it; the local edits were replaced"
			if err := write(SyncOverwritten); err != nil {
				failed = err
				break each
			}
		case f.Status == DriftConflict:
			row.Action, row.Detail = SyncConflict, "edited here and changed in the preset; left as it is"
			if err := s.theirs(&row, opts, d.realTarget, rf); err != nil {
				failed = err
				break each
			}
		case f.Status == DriftLocallyModified:
			row.Action = SyncKeptLocal
		case f.Status == DriftRemovedUpstream:
			row.Action, row.Detail = SyncRemovedUpstream, "the preset no longer renders it; left as it is"
		case f.Status == DriftMissingLocally:
			row.Action, row.Detail = SyncMissingLocally, "deleted here; not restored"
		}
		res.Files = append(res.Files, row)
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	if opts.Plan {
		return res, nil
	}

	refs := make([]manifest.PresetRef, 0, len(d.layers))
	synced := make([]manifest.PresetRef, 0, len(d.layers))
	for _, l := range d.layers {
		refs = append(refs, manifest.PresetRef{ID: l.Preset.ID, Version: l.Preset.Version, Source: l.Source, ResolvedPath: l.ResolvedPath})
		synced = append(synced, manifest.PresetRef{ID: l.Preset.ID, Version: l.Preset.Version})
	}
	if failed == nil {
		// A sync that stopped part-way keeps the versions it started from: what it did
		// not reach was not brought up to them.
		m.Presets = refs
		m.Inputs = root.Ctx.Inputs
		m.Computed = root.Ctx.Computed
		m.SyncHistory = append(m.SyncHistory, manifest.SyncEvent{At: s.now(), Operation: "sync", Presets: synced})
	}
	m.Files = files
	if err := manifest.Write(opts.TargetDir, m); err != nil {
		return SyncResult{}, newErr(ErrWriteFailed, "write manifest", err)
	}
	if failed != nil {
		return res, failed
	}
	return res, nil
}

// theirs writes the preset's new rendering beside a conflicting file, when asked to.
func (s *Service) theirs(row *SyncFile, opts SyncOptions, realTarget string, rf renderedFile) error {
	if opts.TheirsTo == "" {
		return nil
	}
	row.Theirs = row.Path + opts.TheirsTo
	if opts.Plan {
		return nil
	}
	return s.writeProjectFile(realTarget, row.Theirs, rf)
}

// syncPolicy is the policy that applies to rel: the first matching rule of the root
// preset, then of the layer that renders it; else that layer's default, else the root's.
func syncPolicy(rel string, root, owner *preset.Preset) string {
	for _, p := range []*preset.Preset{root, owner} {
		if p == nil || p.Sync == nil {
			continue
		}
		for _, r := range p.Sync.Rules {
			if globMatch(r.Glob, rel) {
				return r.Policy
			}
		}
	}
	for _, p := range []*preset.Preset{owner, root} {
		if p != nil && p.Sync != nil && p.Sync.Default != "" {
			return p.Sync.Default
		}
	}
	return ""
}

// globMatch matches a slash-separated path against a path.Match pattern in which a
// "**" segment matches any number of segments (including none).
func globMatch(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// projectPath is rel inside the project, refused when it (or a directory on the way)
// leads out of it through a symlink.
func projectPath(realTarget, rel string) (string, error) {
	if !fs.ValidPath(rel) || rel == "." {
		return "", newErr(ErrWriteFailed, fmt.Sprintf("%q is not a path inside the project", rel), nil)
	}
	full := filepath.Join(realTarget, filepath.FromSlash(rel))
	// The deepest part of the path that exists must resolve inside the project.
	probe := full
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			if !within(realTarget, resolved) {
				return "", newErr(ErrWriteFailed, fmt.Sprintf("%s leads out of the project through a symlink; not written", rel), nil)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || !within(realTarget, parent) {
			break
		}
		probe = parent
	}
	return full, nil
}

func (s *Service) readProjectFile(realTarget, rel string) ([]byte, error) {
	full, err := projectPath(realTarget, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// writeProjectFile writes one rendered file into the project, through a temporary file
// renamed into place, with the mode and line endings New uses.
func (s *Service) writeProjectFile(realTarget, rel string, rf renderedFile) error {
	full, err := projectPath(realTarget, rel)
	if err != nil {
		return err
	}
	content := rf.File.Content
	if !rf.File.IsBinary {
		content = manifest.NormalizeLineEndings(content)
	}
	if merr := os.MkdirAll(filepath.Dir(full), 0o755); merr != nil {
		return newErr(ErrWriteFailed, "mkdir for "+rel, merr)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".folio-sync-*")
	if err != nil {
		return newErr(ErrWriteFailed, "write "+rel, err)
	}
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), modeFor(rf.File))
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), full)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return newErr(ErrWriteFailed, "write "+rel, werr)
	}
	return nil
}
