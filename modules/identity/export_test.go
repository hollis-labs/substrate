package identity

import "database/sql"

// DBForTest exposes the database handle to the external tests.
func (s *Service) DBForTest() *sql.DB { return s.db.st.DB() }

// OrEmptyForTest exposes orEmpty to the external tests.
func OrEmptyForTest[T any](l []T) []T { return orEmpty(l) }
