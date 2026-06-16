// Package config читає конфігурацію NTP-дашборду з середовища та прапорців.
package config

import (
	"os"
	"strconv"
)

// Config — параметри запуску NTP-дашборду.
type Config struct {
	// DBPath — шлях до SQLite-файлу. Береться з NTP_DB, за замовчуванням ntp.db.
	DBPath string
	// ListenAddr — HTTP-адреса. Береться з NTP_ADDR, за замовчуванням :8080.
	ListenAddr string
	// IntervalMin — мінімальний інтервал між NTP-семплами (секунди).
	IntervalMin int
	// IntervalMax — максимальний інтервал між NTP-семплами (секунди).
	IntervalMax int
}

// Load повертає конфіг із середовища з типовими значеннями.
func Load() Config {
	return Config{
		DBPath:      getenv("NTP_DB", "ntp.db"),
		ListenAddr:  getenv("NTP_ADDR", ":8080"),
		IntervalMin: getenvInt("NTP_INTERVAL_MIN", 30),
		IntervalMax: getenvInt("NTP_INTERVAL_MAX", 120),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
