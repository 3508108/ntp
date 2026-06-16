package store

import "time"

// InsertSample записує один NTP-семпл у ntp_samples і повертає сгенерований sample map-запис.
// Параметри ok/error передаються як у Python-реалізації — error обрізається до 120 символів.
type SampleRow struct {
	Server    string
	OffsetMs  *float64
	DelayMs   *float64
	Stratum   *int
	RandIdx   int
	NextSec   int
	OK        bool
	Error     string
	RandSrc   string
	Timestamp time.Time
}

// InsertSample записує семпл у БД.
func (s *Store) InsertSample(r SampleRow) error {
	okInt := 0
	if r.OK {
		okInt = 1
	}
	errStr := ""
	if len(r.Error) > 120 {
		errStr = r.Error[:120]
	} else {
		errStr = r.Error
	}
	_, err := s.DB.Exec(
		`INSERT INTO ntp_samples (server_host, offset_ms, delay_ms, stratum,
		    rand_idx, next_sec, ok, error, rand_src, ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Server, r.OffsetMs, r.DelayMs, r.Stratum,
		r.RandIdx, r.NextSec, okInt, errStr, r.RandSrc, r.Timestamp.Unix(),
	)
	return err
}

// ClearSamples очищає таблицю ntp_samples (аналог Python db_clear).
func (s *Store) ClearSamples() error {
	_, err := s.DB.Exec("DELETE FROM ntp_samples")
	return err
}

// InsertHeartbeat додає один heartbeat.
func (s *Store) InsertHeartbeat(ts time.Time) error {
	_, err := s.DB.Exec("INSERT INTO heartbeat (ts) VALUES (?)", ts.Unix())
	return err
}

// PruneHeartbeats залишає лише останні 5000 heartbeat-записів (як у Python).
func (s *Store) PruneHeartbeats() error {
	_, err := s.DB.Exec(
		"DELETE FROM heartbeat WHERE id NOT IN " +
			"(SELECT id FROM heartbeat ORDER BY ts DESC LIMIT 5000)",
	)
	return err
}

// LastHeartbeat повертає мітку часу останнього heartbeat; якщо порожньо — zero time і false.
func (s *Store) LastHeartbeat() (time.Time, bool, error) {
	row := s.DB.QueryRow("SELECT ts FROM heartbeat ORDER BY ts DESC LIMIT 1")
	var ts float64
	if err := row.Scan(&ts); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	return time.Unix(int64(ts), 0), true, nil
}

// InsertDowntime записує downtime-подію.
func (s *Store) InsertDowntime(startedAt, endedAt time.Time, durationS float64, reason string) error {
	_, err := s.DB.Exec(
		"INSERT INTO downtime_log (started_at, ended_at, duration_s, reason) VALUES (?, ?, ?, ?)",
		startedAt.Unix(), endedAt.Unix(), durationS, reason,
	)
	return err
}
