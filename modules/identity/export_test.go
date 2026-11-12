package identity

import (
	"database/sql"
	"io"
	"time"
)

// DBForTest exposes the database handle to the external tests.
func (s *Service) DBForTest() *sql.DB { return s.db.st.DB() }

// OrEmptyForTest exposes orEmpty to the external tests.
func OrEmptyForTest[T any](l []T) []T { return orEmpty(l) }

// SetTerminalForTest swaps the terminal detection and no-echo read the offline
// commands use, and returns a function that restores them.
func SetTerminalForTest(isTerminal func(io.Reader) (int, bool), readNoEcho func(int) ([]byte, error)) func() {
	oi, or := terminal.isTerminal, terminal.readNoEcho
	terminal.isTerminal, terminal.readNoEcho = isTerminal, readNoEcho
	return func() { terminal.isTerminal, terminal.readNoEcho = oi, or }
}

// MintForTest signs a short-lived token for sub with the active key, so a test can
// check which key a process ended up with.
func (s *Service) MintForTest(sub string) (string, error) {
	return s.mint(s.newClaims(sub, "user", s.cfg.audience, []string{"*"}, time.Minute, time.Now()))
}
