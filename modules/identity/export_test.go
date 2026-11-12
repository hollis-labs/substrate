package identity

import "database/sql"

// DBForTest exposes the database handle to the external tests.
func (s *Service) DBForTest() *sql.DB { return s.db.st.DB() }
