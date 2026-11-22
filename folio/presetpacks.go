package folio

import (
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
)

// WithPresetPacks mounts additional trusted preset catalogs beside the bundled
// presets. Each filesystem must contain presets/<id>/preset.yaml. Duplicate
// directory names are rejected, never shadowed. Composition keeps its existing
// relative-path and version checks across the combined catalog.
// Callers select immutable checkouts or extracted release packs themselves;
// this function performs no downloads or dependency resolution.
func WithPresetPacks(base fs.FS, packs ...fs.FS) (fs.FS, error) {
	if len(packs) == 0 {
		return base, nil
	}
	m := &presetPacks{base: base, owners: map[string]fs.FS{}}
	sources := append([]fs.FS{base}, packs...)
	for i, source := range sources {
		if source == nil {
			if i == 0 {
				continue
			}
			return nil, fmt.Errorf("preset catalog %d is nil", i)
		}
		entries, err := fs.ReadDir(source, "presets")
		if err != nil {
			return nil, fmt.Errorf("preset catalog %d: %w", i, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id := entry.Name()
			if _, exists := m.owners[id]; exists {
				return nil, fmt.Errorf("duplicate preset directory %q in catalog %d", id, i)
			}
			m.owners[id] = source
			m.entries = append(m.entries, entry)
		}
		if m.base == nil {
			m.base = source
		}
	}
	slices.SortFunc(m.entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return m, nil
}

type presetPacks struct {
	base    fs.FS
	owners  map[string]fs.FS
	entries []fs.DirEntry
}

func (m *presetPacks) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "presets" {
		f, err := m.base.Open(name)
		if err != nil {
			return nil, err
		}
		return &presetDirectory{File: f, entries: m.entries}, nil
	}
	if rest, ok := strings.CutPrefix(name, "presets/"); ok {
		id, _, _ := strings.Cut(rest, "/")
		if source, ok := m.owners[id]; ok {
			return source.Open(name)
		}
	}
	return m.base.Open(name)
}

type presetDirectory struct {
	fs.File
	entries []fs.DirEntry
	offset  int
}

func (d *presetDirectory) ReadDir(n int) ([]fs.DirEntry, error) {
	if n > 0 && d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := len(d.entries)
	if n > 0 && n < end-d.offset {
		end = d.offset + n
	}
	out := slices.Clone(d.entries[d.offset:end])
	d.offset = end
	return out, nil
}
