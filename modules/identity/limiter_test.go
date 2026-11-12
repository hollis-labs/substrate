package identity

import (
	"testing"
	"time"
)

func TestLimiterWaitIsTheTimeUntilTheBlockLifts(t *testing.T) {
	l := newLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if w := l.wait("k", t0); w != 0 {
		t.Fatalf("fresh key waits %v", w)
	}
	for i := 0; i < maxFails-1; i++ {
		l.fail("k", t0.Add(time.Duration(i)*time.Minute))
	}
	if w := l.wait("k", t0.Add(4*time.Minute)); w != 0 {
		t.Fatalf("below the limit waits %v", w)
	}
	l.fail("k", t0.Add(4*time.Minute)) // fifth failure: blocked
	now := t0.Add(4*time.Minute + 30*time.Second)
	// the oldest failure (t0) leaves the window at t0+5m, which drops the count to 4
	if w, want := l.wait("k", now), 30*time.Second; w != want {
		t.Fatalf("wait = %v, want %v", w, want)
	}
	// a sixth failure: now two must leave the window before the count is 4
	l.fail("k", t0.Add(4*time.Minute+10*time.Second))
	if w, want := l.wait("k", now), 90*time.Second; w != want {
		t.Fatalf("with six failures wait = %v, want %v", w, want)
	}
	if w := l.wait("k", t0.Add(window+time.Minute)); w != 0 {
		t.Fatalf("after two have left the window waits %v", w)
	}
}
