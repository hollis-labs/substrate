package identity

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// maxPasswordBytes is bcrypt's input limit; a longer password is refused rather
// than silently truncated.
const maxPasswordBytes = 72

func (s *Service) checkPassword(pw string) error {
	switch {
	case len(pw) > maxPasswordBytes:
		return fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
	case utf8.RuneCountInString(pw) < s.cfg.minPassword:
		return fmt.Errorf("password must be at least %d characters", s.cfg.minPassword)
	case strings.TrimSpace(pw) == "":
		return errors.New("password must not be blank")
	}
	return nil
}

func (s *Service) hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), s.cfg.cost)
	return string(h), err
}

// dummyHash makes a sign-in for an unknown user cost as much as a known one.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("identity-timing-pad"), bcrypt.MinCost)

func (s *Service) verifyPassword(hash, pw string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// limiter slows guessing: after maxFails failures inside window for one key
// (username plus client address), attempts are refused until the window passes.
type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

const (
	maxFails = 5
	window   = 5 * time.Minute
)

func newLimiter() *limiter { return &limiter{fails: map[string][]time.Time{}} }

func (l *limiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= maxFails
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.recent(key, now), now)
}

func (l *limiter) clear(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}

func (l *limiter) recent(key string, now time.Time) []time.Time {
	keep := l.fails[key][:0:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = keep
	}
	return keep
}
