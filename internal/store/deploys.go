package store

import "time"

// DeployedRow — подія деплою, як у deploy_log.
type DeployedRow struct {
	DeployedAt  time.Time
	DurationMs  *int
	GitHash     string
	Message     string
}

// InsertDeploy записує подію деплою.
func (s *Store) InsertDeploy(r DeployedRow) error {
	msg := r.Message
	if len(msg) > 120 {
		msg = msg[:120]
	}
	hashVal := r.GitHash
	if len(hashVal) > 12 {
		hashVal = hashVal[:12]
	}
	_, err := s.DB.Exec(
		"INSERT INTO deploy_log (deployed_at, duration_ms, git_hash, message) VALUES (?, ?, ?, ?)",
		r.DeployedAt.Unix(), r.DurationMs, hashVal, msg,
	)
	return err
}
