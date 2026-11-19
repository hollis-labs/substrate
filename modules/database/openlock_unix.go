//go:build unix

package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockPollInterval is how often a waiting process retries the advisory lock.
const lockPollInterval = 10 * time.Millisecond

// acquireOpenLock takes an exclusive flock(2) on "<dbpath>.open.lock" and
// returns the release func. The kernel drops the lock when the holding process
// exits, however it exits, so a crashed process never leaves it held; the file
// itself is left in place and its existence means nothing. A wait longer than
// timeout (or a cancelled ctx) is an error naming the lock file.
func acquireOpenLock(ctx context.Context, dbPath string, timeout time.Duration) (func(), error) {
	lockPath := dbPath + ".open.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("database: open lock: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("database: open lock %s: %w", lockPath, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, fmt.Errorf("database: open lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("database: another process held the open lock %s for more than %s (open and migrate of %s in progress, or stuck); raise database.sqlite.open_lock_timeout or find the process holding it", lockPath, timeout, dbPath)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("database: waiting for open lock %s: %w", lockPath, ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}
