// Package store реалізує роботу з SQLite для NTP-дашборду:
//
//   - ініціалізація схеми та індексів;
//   - міграція (додавання колонки rand_src, якщо вона відсутня);
//   - WAL-режим і таймаут для busy-стану;
//   - запис NTP-семплів, heartbeat, downtime і deploy-логу.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite без CGO
)

// Schema — DDL для всіх таблиць і індексів, відповідає Python ntp_sampler.py.
const Schema = `
CREATE TABLE IF NOT EXISTS ntp_samples (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    server_host TEXT    NOT NULL,
    offset_ms   REAL,
    delay_ms    REAL,
    stratum     INTEGER,
    rand_idx    INTEGER,
    next_sec    INTEGER,
    ok          INTEGER NOT NULL DEFAULT 1,
    error       TEXT,
    ts          REAL    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ntp_ts ON ntp_samples(ts);

CREATE TABLE IF NOT EXISTS heartbeat (
    id  INTEGER PRIMARY KEY AUTOINCREMENT,
    ts  REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hb_ts ON heartbeat(ts);

CREATE TABLE IF NOT EXISTS downtime_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  REAL NOT NULL,
    ended_at    REAL NOT NULL,
    duration_s  REAL NOT NULL,
    reason      TEXT DEFAULT 'service_restart'
);

CREATE TABLE IF NOT EXISTS deploy_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    deployed_at REAL    NOT NULL,
    duration_ms INTEGER,
    git_hash    TEXT,
    message     TEXT
);
`

// Store — обгортка над *sql.DB з усіма операціями для NTP-дашборду.
type Store struct {
	DB *sql.DB
}

// Open відкриває SQLite-файл у режимі WAL і таймаутом 3с (як у Python).
// Ініціалізує схему та індекси. Додає колонку rand_src у ntp_samples, якщо її немає.
func Open(path string) (*Store, error) {
	// таймаут на busy + WAL через DSN
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	if _, err := s.DB.Exec(Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	// міграція: якщо ntp_samples не має rand_src — додати (ідентично Python-логіці)
	if err := s.ensureRandSrcColumn(); err != nil {
		return err
	}
	return nil
}

// TotalSamples повертає кількість записів у ntp_samples (використовується для status.total під час старту).
func (s *Store) TotalSamples() (int64, error) {
	row := s.DB.QueryRow("SELECT COUNT(*) FROM ntp_samples")
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Close закриває з'єднання з БД.
func (s *Store) Close() error {
	return s.DB.Close()
}
