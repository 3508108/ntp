package fetcher

import (
	"path/filepath"
	"testing"
	"time"

	"ntp/easy/internal/store"
)

func newTestFetcher(t *testing.T, interval time.Duration) *Fetcher {
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

	return New(db, interval)
}

func TestNewClampsSmallIntervalToDefault(t *testing.T) {
	f := newTestFetcher(t, time.Second)

	if got, want := f.Interval(), 10*time.Second; got != want {
		t.Fatalf("Interval() = %v, want %v", got, want)
	}
}

func TestSetIntervalClampsSmallIntervalToMinimum(t *testing.T) {
	f := newTestFetcher(t, 30*time.Second)

	f.SetInterval(time.Second)

	if got, want := f.Interval(), 5*time.Second; got != want {
		t.Fatalf("Interval() = %v, want %v", got, want)
	}
}

func TestSetIntervalStoresValidInterval(t *testing.T) {
	f := newTestFetcher(t, 30*time.Second)

	f.SetInterval(45 * time.Second)

	if got, want := f.Interval(), 45*time.Second; got != want {
		t.Fatalf("Interval() = %v, want %v", got, want)
	}
}
