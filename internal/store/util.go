package store

import (
	"database/sql"
	"os"
	"path/filepath"
)

// DBSizeKB повертає розмір файлу БД у кілобайтах (з округленням до 1 знака), як у Python status().
func DBSizeKB(path string) float64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	kb := float64(fi.Size()) / 1024.0
	// округлення до 1 знака як у Python round()
	return roundTo1(kb)
}

// DirSizeKB — розмір каталогу (на випадок, якщо БД розщеплена на WAL-файли).
func DirSizeKB(path string) float64 {
	dir := filepath.Dir(path)
	var total int64
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return roundTo1(float64(total) / 1024.0)
}

func roundTo1(x float64) float64 {
	// Math round to one decimal: x * 10, round, /10.
	n := int(x*10 + 0.5)
	if x < 0 {
		n = int(x*10 - 0.5)
	}
	return float64(n) / 10
}

// AsSQLDB відкриває додаткове з'єднання до тієї самої БД (для backup API).
// Використовується для експорту БД у тимчасовий файл.
func (s *Store) AsSQLDB() (*sql.DB, error) {
	return sql.Open("sqlite", s.dsnForBackup())
}

// dsnForBackup повертає DSN, що відповідає поточному з'єднанню (без WAL-управління).
func (s *Store) dsnForBackup() string {
	// спрощено: використовуємо лише file path; modernc.org/sqlite приймає просто path.
	return _lastOpenPath
}

// _lastOpenPath зберігає шлях, що використовувався в Open; змінюється при ініціалізації.
// Потрібен, щоб open додаткове з'єднання з тим самим файлом без конфігурації DSN.
var _lastOpenPath = ""

// SetOpenPath зберігає шлях для подальшого використання в AsSQLDB.
func (s *Store) SetOpenPath(path string) {
	_lastOpenPath = path
}
