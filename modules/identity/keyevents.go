package identity

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// KeyEvent is an API key made or revoked, for an app that keeps a record of it (an activity
// feed, an audit log). It never carries the secret.
type KeyEvent struct {
	Action string     `json:"action"` // KeyCreated or KeyRevoked
	Key    Credential `json:"key"`
	// By is who did it: the principal that called the route ("user:<id>", "key:<id>"), or
	// the key's CreatedBy for an in-process CreateKey, or "" when unknown (in-process revoke).
	By string    `json:"by,omitempty"`
	At time.Time `json:"at"`
}

// Key event actions.
const (
	KeyCreated = "created"
	KeyRevoked = "revoked"
)

type keyWatchers struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(KeyEvent)
}

// WatchKeys calls fn after each API key is made (by any route, CreateKey or a redeemed pairing
// code) and after each key is revoked (only when it was live: revoking twice reports once). fn
// runs on the caller's goroutine after the change is stored; it must not block.
func (s *Service) WatchKeys(fn func(KeyEvent)) (stop func()) {
	w := &s.keyWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fns == nil {
		w.fns = map[int]func(KeyEvent){}
	}
	id := w.next
	w.next++
	w.fns[id] = fn
	return func() {
		w.mu.Lock()
		delete(w.fns, id)
		w.mu.Unlock()
	}
}

func (s *Service) emitKey(action string, c Credential, by string) {
	w := &s.keyWatch
	w.mu.Lock()
	ids := slices.Sorted(maps.Keys(w.fns))
	fns := make([]func(KeyEvent), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, w.fns[id])
	}
	w.mu.Unlock()
	if len(fns) == 0 {
		return
	}
	e := KeyEvent{Action: action, Key: c, By: by, At: time.Now().UTC()}
	for _, fn := range fns {
		fn(e)
	}
}

// actor is the verified principal on ctx, "" without one.
func actor(ctx context.Context) string {
	if p, ok := libauth.FromContext(ctx); ok && p.Verified {
		return p.ID
	}
	return ""
}
