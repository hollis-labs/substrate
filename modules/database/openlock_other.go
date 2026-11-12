//go:build !unix

package database

import (
	"context"
	"time"
)

// acquireOpenLock is a no-op where flock(2) is not available: concurrent first
// starts on one new SQLite file are not serialised there (see the store
// contract in docs/adopting-substrate.md).
func acquireOpenLock(context.Context, string, time.Duration) (func(), error) {
	return func() {}, nil
}
