package store

import "database/sql"

// ensureRandSrcColumn додає колонку rand_src до ntp_samples, якщо її ще немає.
// Це ідентично Python-логіці в ntp_sampler.py:
//   cols = [r[1] for r in conn.execute("PRAGMA table_info(ntp_samples)")]
//   if "rand_src" not in cols: ALTER TABLE ntp_samples ADD COLUMN rand_src TEXT
func (s *Store) ensureRandSrcColumn() error {
	rows, err := s.DB.Query("PRAGMA table_info(ntp_samples)")
	if err != nil {
		return err
	}
	defer rows.Close()
	colNames := map[string]bool{}
	for rows.Next() {
		var (
			cid      int
			name     sql.NullString
			ctype    sql.NullString
			notnull  sql.NullInt64
			dfltVal  sql.NullString
			pk       sql.NullInt64
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltVal, &pk); err != nil {
			return err
		}
		if name.Valid {
			colNames[name.String] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !colNames["rand_src"] {
		if _, err := s.DB.Exec("ALTER TABLE ntp_samples ADD COLUMN rand_src TEXT"); err != nil {
			return err
		}
	}
	return nil
}
