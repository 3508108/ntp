package store

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "easy.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	return db
}

func TestInsertAndRecent(t *testing.T) {
	db := openTestDB(t)

	if err := db.Insert("apple", "2026-06-21 12:00:00.000", 1000, 995, 0, "time.apple.com"); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	rows, err := db.Recent(10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Recent() len = %d, want 1", len(rows))
	}

	row := rows[0]
	if row.Probe != "apple" || row.UnixMs != 1000 || row.ServerMs != 995 || row.NtpName != "time.apple.com" {
		t.Fatalf("Recent() row = %+v", row)
	}
	if row.ID == 0 || row.CreatedAt == 0 {
		t.Fatalf("Recent() did not populate ID/CreatedAt: %+v", row)
	}
}

func TestInsertPing0000AndRecent(t *testing.T) {
	db := openTestDB(t)

	if err := db.InsertPing0000("12:00", "1000", "phone", "ping"); err != nil {
		t.Fatalf("InsertPing0000() error = %v", err)
	}

	rows, err := db.RecentPing0000(10)
	if err != nil {
		t.Fatalf("RecentPing0000() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("RecentPing0000() len = %d, want 1", len(rows))
	}

	row := rows[0]
	if row.TimeStr != "12:00" || row.Timestamp != "1000" || row.Device != "phone" || row.Action != "ping" {
		t.Fatalf("RecentPing0000() row = %+v", row)
	}
}
