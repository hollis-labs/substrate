package identity

import "time"

// SetClockForTest replaces the clock that decides when a retired key leaves the
// set. Call it before concurrent use.
func (s *Service) SetClockForTest(f func() time.Time) { s.now = f }

// SetFaultForTest installs a hook called at the rotation steps "before-commit"
// (retired record written, new key not yet renamed in; a returned error aborts),
// "after-commit" and "after-jwks" (past the commit point; the return value is
// ignored, a panic simulates a crash). Hooks must not register modules.
func (s *Service) SetFaultForTest(f func(point string) error) { s.fault = f }

// PruneForTest runs the hourly sweep's key step.
func (s *Service) PruneForTest() error { return s.prune() }
