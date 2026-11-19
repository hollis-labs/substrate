package gateway

// These adapters are deliberately test-only. Applications supply their own
// durable journal and surface store; this package installs no production store.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	libstore "github.com/hollis-labs/libs/core/store"
)

type fileStore struct {
	db           *sql.DB
	close        func() error
	failResult   atomic.Bool
	loseClaimAck atomic.Bool
}

func openFileStore(t *testing.T, path string) *fileStore {
	t.Helper()
	store, err := libstore.Open(t.Context(), libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: path, Synchronous: "FULL", Journal: "wal"}})
	if err != nil {
		t.Fatal(err)
	}
	db := store.DB()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000",
		"CREATE TABLE IF NOT EXISTS actions (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, digest TEXT NOT NULL, claimed INTEGER NOT NULL, record BLOB NOT NULL)",
		"CREATE TABLE IF NOT EXISTS surfaces (key TEXT PRIMARY KEY, record BLOB NOT NULL)",
		"CREATE TABLE IF NOT EXISTS effects (action_id TEXT NOT NULL)",
	} {
		if _, err = db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = store.Close() })
	return &fileStore{db: db, close: store.Close}
}
func newFileStore(t *testing.T) *fileStore {
	return openFileStore(t, filepath.Join(t.TempDir(), "journal.db"))
}
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}
func dbKey(v any) string { return string(encode(v)) }
func (f *fileStore) Begin(ctx context.Context, k Key, r Record) (Record, bool, error) {
	result, err := f.db.ExecContext(ctx, "INSERT INTO actions(key,revision,digest,claimed,record) VALUES(?,?,?,?,?) ON CONFLICT(key) DO NOTHING", dbKey(k), r.Revision, r.Digest, r.Claimed, encode(r))
	if err != nil {
		return Record{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Record{}, false, err
	}
	got, err := f.Load(ctx, k)
	return got, n == 1, err
}
func (f *fileStore) Load(ctx context.Context, k Key) (Record, error) {
	var raw []byte
	err := f.db.QueryRowContext(ctx, "SELECT record FROM actions WHERE key=?", dbKey(k)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	err = decode(raw, &r)
	return r, err
}
func (f *fileStore) CompareAndSwap(ctx context.Context, k Key, rev uint64, next Record) (Record, bool, error) {
	if next.Revision != rev+1 {
		return Record{}, false, fmt.Errorf("bad revision")
	}
	if next.Claimed && next.Phase != PhaseClaimed && f.failResult.Load() {
		return Record{}, false, fmt.Errorf("injected result storage failure")
	}
	result, err := f.db.ExecContext(ctx, "UPDATE actions SET revision=?,claimed=?,record=? WHERE key=? AND revision=? AND digest=? AND (claimed=0 OR ?=1)", next.Revision, next.Claimed, encode(next), dbKey(k), rev, next.Digest, next.Claimed)
	if err != nil {
		return Record{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Record{}, false, err
	}
	if n == 1 && next.Phase == PhaseClaimed && f.loseClaimAck.Load() {
		return Record{}, false, fmt.Errorf("injected lost durable claim acknowledgement")
	}
	r, err := f.Load(ctx, k)
	return r, n == 1, err
}

type surfacePort struct{ *fileStore }

func surfaceKey(scope Scope, id string) string {
	return dbKey(struct {
		Scope Scope
		ID    string
	}{scope, id})
}
func (f surfacePort) Put(ctx context.Context, s Surface) error {
	k := surfaceKey(s.Scope, s.Envelope.ID)
	raw := encode(s)
	_, err := f.db.ExecContext(ctx, "INSERT INTO surfaces(key,record) VALUES(?,?) ON CONFLICT(key) DO NOTHING", k, raw)
	if err != nil {
		return err
	}
	var current []byte
	if err = f.db.QueryRowContext(ctx, "SELECT record FROM surfaces WHERE key=?", k).Scan(&current); err != nil {
		return err
	}
	if !bytes.Equal(current, raw) {
		return ErrInvalid
	}
	return nil
}
func (f surfacePort) Get(ctx context.Context, scope Scope, id string) (Surface, error) {
	var raw []byte
	err := f.db.QueryRowContext(ctx, "SELECT record FROM surfaces WHERE key=?", surfaceKey(scope, id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Surface{}, ErrNotFound
	}
	if err != nil {
		return Surface{}, err
	}
	var s Surface
	err = decode(raw, &s)
	return s, err
}
func (f surfacePort) Supersede(ctx context.Context, scope Scope, id, generation string) error {
	s, err := f.Get(ctx, scope, id)
	if err != nil {
		return err
	}
	if s.Generation != generation {
		return ErrInvalid
	}
	old := encode(s)
	s.Superseded = true
	r, err := f.db.ExecContext(ctx, "UPDATE surfaces SET record=? WHERE key=? AND record=?", encode(s), surfaceKey(scope, id), old)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n != 1 {
		return ErrInvalid
	}
	return err
}
func (f *fileStore) effectCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM effects").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
