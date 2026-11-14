package database

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

func TestTargetOfNeverIncludesCredentials(t *testing.T) {
	pw := "p" + "w0rd"
	for dsn, want := range map[string]string{
		"postgres://user:" + pw + "@db.example:5432/app?sslmode=verify-full": "db.example:5432/app",
		"postgresql://user@db.example/app":                                   "db.example/app",
		"host=db.example port=5433 user=u password=" + pw + " dbname=app":    "db.example:5433/app",
		"host='db.example' dbname='app'":                                     "db.example/app",
		"nonsense":                                                           "postgres",
	} {
		got := targetOf(store.Config{Driver: store.DriverPostgres, Postgres: store.PostgresConfig{DSN: dsn}})
		if got != want || strings.Contains(got, pw) {
			t.Errorf("targetOf(%q) = %q, want %q", dsn, got, want)
		}
	}
	if got := targetOf(store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: "/var/lib/app/data.db"}}); got != "data.db" {
		t.Errorf("sqlite target = %q", got)
	}
}

func TestRedactErrorMasksCredentials(t *testing.T) {
	pw := "s3" + "cr3t"
	for _, in := range []string{
		"dial postgres://app:" + pw + "@db:5432/x: refused",
		"failed host=db user=app password=" + pw + " dbname=x",
		"PASSWORD='" + pw + "' bad",
	} {
		if out := redactError(errors.New(in)); strings.Contains(out, pw) {
			t.Errorf("redactError(%q) = %q leaks the secret", in, out)
		}
	}
	if long := redactError(errors.New(strings.Repeat("x", 1000))); len(long) > 245 {
		t.Errorf("not truncated: %d", len(long))
	}
}

func TestBackoffGrowsToItsCapAndJitterStaysClose(t *testing.T) {
	b := backoff{initial: time.Second, max: 30 * time.Second}
	d := b.initial
	var seen []time.Duration
	for i := 0; i < 8; i++ {
		d = b.next(d)
		seen = append(seen, d)
	}
	if seen[0] != 2*time.Second || seen[len(seen)-1] != 30*time.Second {
		t.Fatalf("backoff sequence = %v", seen)
	}
	for i := 0; i < 200; i++ {
		j := jitter(10 * time.Second)
		if j < 8*time.Second || j > 12*time.Second {
			t.Fatalf("jitter %v outside 8s..12s", j)
		}
	}
}
