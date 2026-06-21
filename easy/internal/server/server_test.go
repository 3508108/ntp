package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ntp/easy/internal/fetcher"
	"ntp/easy/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "easy.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	return New(db, fetcher.New(db, 10*time.Second)), db
}

func TestHandleRecentReturnsSnakeCaseRows(t *testing.T) {
	srv, db := newTestServer(t)
	if err := db.Insert("google", "2026-06-21 12:00:00.000", 2000, 1997, 0, "time.google.com"); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/recent", nil)
	rec := httptest.NewRecorder()
	srv.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(body.Rows))
	}

	row := body.Rows[0]
	for _, key := range []string{"id", "probe", "date_time", "unix_ms", "server_ms", "cloudflare_ms", "ntp_name", "created_at"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("missing JSON key %q in row %#v", key, row)
		}
	}
	if _, ok := row["UnixMs"]; ok {
		t.Fatalf("unexpected Go-style JSON key UnixMs in row %#v", row)
	}
}

func TestHandleSetInterval(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/interval", strings.NewReader(`{"interval":"30s"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := srv.fetcher.Interval(), 30*time.Second; got != want {
		t.Fatalf("Interval() = %v, want %v", got, want)
	}
}

func TestHandleSetIntervalRejectsInvalidDuration(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/interval", strings.NewReader(`{"interval":"soon"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandle0000StoresPing(t *testing.T) {
	srv, db := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/0000", strings.NewReader(`{"time":"12:00","timestamp":"1000","device":"phone","action":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rows, err := db.RecentPing0000(10)
	if err != nil {
		t.Fatalf("RecentPing0000() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Device != "phone" || rows[0].Action != "ping" {
		t.Fatalf("RecentPing0000() rows = %+v", rows)
	}
}
