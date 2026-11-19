package database

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
)

// reConn matches the Postgres errors that mean "the server is not there or not
// taking connections": connection exceptions (class 08), administrator or crash
// shutdown and "cannot connect now" (57P01 to 57P03), too many connections (53300).
var reConn = regexp.MustCompile(`SQLSTATE (08[0-9A-Z]{3}|57P0[1-3]|53300)`)

var connPhrases = []string{"bad connection", "connection reset", "connection refused", "broken pipe",
	"closed pool", "connection closed", "server closed the connection", "failed to connect", "conn closed"}

// IsUnavailable reports whether err means the database cannot be reached, as
// opposed to a query that failed on its own merits: ErrUnavailable from Get, a
// closed or reset connection, a dial failure, or a Postgres shutdown or
// connection-class error. A handler maps it to the same 503 as Gate, and passes
// it to Handle.Report so the state flips without waiting for the next ping.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	switch {
	case errors.Is(err, ErrUnavailable), errors.Is(err, driver.ErrBadConn), errors.Is(err, sql.ErrConnDone),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE),
		errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ETIMEDOUT),
		errors.As(err, &ne):
		return true
	}
	if reConn.MatchString(err.Error()) {
		return true
	}
	low := strings.ToLower(err.Error())
	for _, p := range connPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}
