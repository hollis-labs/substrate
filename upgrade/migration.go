package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Migration is a mechanical, reviewed change to an app that a release needs.
// Introduced is the release whose "### Breaking" entry names it.
type Migration struct {
	ID          string
	Introduced  string // "vX.Y.Z"
	Description string
	Apply       func(tx *Tx) error
}

// Migrations are the registered mechanical migrations. A release with a
// breaking change must add one (or mark the entry "(migration: none)" and
// describe the manual step) before it is tagged.
var Migrations []Migration

// Tx records the original of every file a migration writes so the whole
// upgrade can be undone.
type Tx struct {
	Dir   string
	saved map[string][]byte // path -> original bytes; nil = did not exist
}

// NewTx starts a transaction rooted at dir.
func NewTx(dir string) *Tx { return &Tx{Dir: dir, saved: map[string][]byte{}} }

// Read reads a file under the app dir.
func (t *Tx) Read(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(t.Dir, rel)) }

// Write saves the original, then writes.
func (t *Tx) Write(rel string, data []byte) error {
	path := filepath.Join(t.Dir, rel)
	if _, seen := t.saved[path]; !seen {
		orig, err := os.ReadFile(path)
		switch {
		case err == nil:
			t.saved[path] = orig
		case os.IsNotExist(err):
			t.saved[path] = nil
		default:
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Snapshot saves a file's original without writing it, for files another tool
// (the go command) is about to rewrite.
func (t *Tx) Snapshot(rel string) error {
	path := filepath.Join(t.Dir, rel)
	if _, seen := t.saved[path]; seen {
		return nil
	}
	orig, err := os.ReadFile(path)
	switch {
	case err == nil:
		t.saved[path] = orig
	case os.IsNotExist(err):
		t.saved[path] = nil
	default:
		return err
	}
	return nil
}

// Rollback restores every file the transaction touched.
func (t *Tx) Rollback() error {
	var first error
	for path, orig := range t.saved {
		var err error
		if orig == nil {
			err = os.Remove(path)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = os.WriteFile(path, orig, 0o644)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Changed lists the files the transaction wrote.
func (t *Tx) Changed() []string {
	var out []string
	for p := range t.saved {
		rel, _ := filepath.Rel(t.Dir, p)
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// applicable returns the migrations introduced in (from, to], oldest first.
func applicable(all []Migration, from, to Version) ([]Migration, error) {
	var out []Migration
	for _, m := range all {
		v, err := ParseVersion(m.Introduced)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", m.ID, err)
		}
		if v.Compare(from) > 0 && v.Compare(to) <= 0 {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := ParseVersion(out[i].Introduced)
		b, _ := ParseVersion(out[j].Introduced)
		return a.Compare(b) < 0
	})
	return out, nil
}
