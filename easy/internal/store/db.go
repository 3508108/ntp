package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn      *sql.DB
	writeLock sync.Mutex
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	db := &DB{conn: conn}
	if err := db.init(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) init() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS time_log (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			probe       TEXT    NOT NULL,
			date_time   TEXT    NOT NULL,
			unix_ms     INTEGER NOT NULL,
			server_ms   INTEGER,
			cloudflare_ms INTEGER,
			ntp_name    TEXT,
			created_at  REAL    NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("init time_log: %w", err)
	}
	_, err = db.conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_time_log_created ON time_log(created_at DESC)
	`)
	if err != nil {
		return fmt.Errorf("init idx_time_log: %w", err)
	}
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS ping_0000 (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			time_str    TEXT    NOT NULL,
			timestamp   TEXT    NOT NULL,
			device      TEXT    NOT NULL,
			action      TEXT    NOT NULL,
			created_at  REAL    NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("init ping_0000: %w", err)
	}
	_, err = db.conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_ping_0000_created ON ping_0000(created_at DESC)
	`)
	if err != nil {
		return fmt.Errorf("init idx_ping_0000: %w", err)
	}
	return nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Insert(probe, dateTime string, unixMs, serverMs, cloudflareMs int64, ntpName string) error {
	db.writeLock.Lock()
	defer db.writeLock.Unlock()
	_, err := db.conn.Exec(
		`INSERT INTO time_log (probe, date_time, unix_ms, server_ms, cloudflare_ms, ntp_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		probe, dateTime, unixMs, serverMs, cloudflareMs, ntpName, time.Now().UnixNano()/1e6,
	)
	return err
}

type Row struct {
	ID           int64   `json:"id"`
	Probe        string  `json:"probe"`
	DateTime     string  `json:"date_time"`
	UnixMs       int64   `json:"unix_ms"`
	ServerMs     int64   `json:"server_ms"`
	CloudflareMs int64   `json:"cloudflare_ms"`
	NtpName      string  `json:"ntp_name"`
	CreatedAt    float64 `json:"created_at"`
}

func (db *DB) Recent(n int) ([]Row, error) {
	rows, err := db.conn.Query(
		`SELECT id, probe, date_time, unix_ms, server_ms, cloudflare_ms, ntp_name, created_at
		 FROM time_log ORDER BY created_at DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var r Row
		err := rows.Scan(&r.ID, &r.Probe, &r.DateTime, &r.UnixMs, &r.ServerMs, &r.CloudflareMs, &r.NtpName, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) LogsSince(createdAtMs int64) ([]Row, error) {
	query := `SELECT id, probe, date_time, unix_ms, server_ms, cloudflare_ms, ntp_name, created_at
		 FROM time_log`
	args := []any{}
	if createdAtMs > 0 {
		query += ` WHERE created_at >= ?`
		args = append(args, createdAtMs)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var r Row
		err := rows.Scan(&r.ID, &r.Probe, &r.DateTime, &r.UnixMs, &r.ServerMs, &r.CloudflareMs, &r.NtpName, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
