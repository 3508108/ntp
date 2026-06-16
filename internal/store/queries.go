package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SampleReadRow — форма запису ntp_samples, що повертається через /ntp/recent.
type SampleReadRow struct {
	Server    string   `json:"server"`
	OffsetMs  *float64 `json:"offset_ms"`
	DelayMs   *float64 `json:"delay_ms"`
	Stratum   *int     `json:"stratum"`
	RandIdx   int      `json:"rand_idx"`
	NextSec   int      `json:"next_sec"`
	OK        bool     `json:"ok"`
	Error     *string  `json:"error,omitempty"`
	RandSrc   string   `json:"rand_src"`
	Timestamp float64  `json:"ts"`
	TimestampFmt string `json:"ts_fmt"`
}

// RecentSamples повертає останні n NTP-семплів у порядку спадання ts.
func (s *Store) RecentSamples(n int) ([]SampleReadRow, error) {
	rows, err := s.DB.Query(
		`SELECT server_host, offset_ms, delay_ms, stratum,
		        rand_idx, next_sec, ok, error, rand_src, ts
		 FROM ntp_samples ORDER BY ts DESC LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SampleReadRow
	for rows.Next() {
		var (
			server   string
			offsetMs sql.NullFloat64
			delayMs  sql.NullFloat64
			stratum  sql.NullInt64
			randIdx  int
			nextSec  int
			okInt    int
			errMsg   sql.NullString
			randSrc  sql.NullString
			ts       float64
		)
		if err := rows.Scan(&server, &offsetMs, &delayMs, &stratum, &randIdx, &nextSec, &okInt, &errMsg, &randSrc, &ts); err != nil {
			return nil, err
		}
		row := SampleReadRow{
			Server:      server,
			RandIdx:     randIdx,
			NextSec:     nextSec,
			OK:          okInt != 0,
			RandSrc:     "local",
			Timestamp:   ts,
			TimestampFmt: formatHMSS(ts),
		}
		if offsetMs.Valid {
			v := round3(offsetMs.Float64)
			row.OffsetMs = &v
		}
		if delayMs.Valid {
			v := round3(delayMs.Float64)
			row.DelayMs = &v
		}
		if stratum.Valid {
			v := int(stratum.Int64)
			row.Stratum = &v
		}
		if errMsg.Valid {
			v := errMsg.String
			row.Error = &v
		}
		if randSrc.Valid && randSrc.String != "" {
			row.RandSrc = randSrc.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DowntimeEvent — форма downtime-запису для /ntp/downtime.
type DowntimeEvent struct {
	StartedAt   float64 `json:"started_at"`
	endedAt     float64 `json:"ended_at,omitempty"`
	DurationS   float64 `json:"duration_s"`
	Reason      string  `json:"reason"`
	StartedFmt  string  `json:"started_fmt"`
	DateFmt     string  `json:"date_fmt"`
	endedAtPresent bool `json:"-"`
}

// RecentDowntime повертає останні n downtime-подій.
func (s *Store) RecentDowntime(n int) ([]DowntimeEvent, error) {
	rows, err := s.DB.Query(
		`SELECT started_at, ended_at, duration_s, reason
		 FROM downtime_log ORDER BY started_at DESC LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DowntimeEvent
	for rows.Next() {
		var (
			started   float64
			ended     float64
			dur       float64
			reason    sql.NullString
		)
		if err := rows.Scan(&started, &ended, &dur, &reason); err != nil {
			return nil, err
		}
		ev := DowntimeEvent{
			StartedAt:     started,
			DurationS:     dur,
			StartedFmt:    formatHMSS(started),
			DateFmt:       formatDayMon(started),
		}
		if reason.Valid {
			ev.Reason = reason.String
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// DeployEvent — форма для /ntp/deploys і /ntp/deploy.
type DeployEvent struct {
	DeployedAt  float64  `json:"deployed_at"`
	DurationMs  *int     `json:"duration_ms,omitempty"`
	GitHash     string   `json:"git_hash"`
	Message     string   `json:"message"`
	TimestampFmt string  `json:"ts_fmt"`
	DateFmt     string   `json:"date_fmt,omitempty"`
}

// RecentDeploys повертає останні n deploy-подій.
func (s *Store) RecentDeploys(n int) ([]DeployEvent, error) {
	rows, err := s.DB.Query(
		`SELECT deployed_at, duration_ms, git_hash, message
		 FROM deploy_log ORDER BY deployed_at DESC LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployEvent
	for rows.Next() {
		var (
			deployed float64
			durMs    sql.NullInt64
			hash     sql.NullString
			message  sql.NullString
		)
		if err := rows.Scan(&deployed, &durMs, &hash, &message); err != nil {
			return nil, err
		}
		ev := DeployEvent{
			DeployedAt:   deployed,
			GitHash:      "",
			Message:      "",
			TimestampFmt: formatHMSS(deployed),
			DateFmt:      formatDayMon(deployed),
		}
		if durMs.Valid {
			v := int(durMs.Int64)
			ev.DurationMs = &v
		}
		if hash.Valid {
			ev.GitHash = hash.String
		}
		if message.Valid {
			ev.Message = message.String
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// UptimeStats — форма для /ntp/uptime-stats.
type UptimeStats struct {
	UptimePct      float64  `json:"uptime_pct"`
	TotalDown24hS  float64  `json:"total_down_24h_s"`
	Incidents24h   int      `json:"incidents_24h"`
	LastDurationS  *float64 `json:"last_duration_s,omitempty"`
	LastStartedFmt *string  `json:"last_started_fmt,omitempty"`
	MonitoringSince *string `json:"monitoring_since,omitempty"`
}

// ComputeUptimeStats повертає агреговану статистику uptime.
func (s *Store) ComputeUptimeStats(now time.Time) (UptimeStats, error) {
	var stats UptimeStats
	dayStart := now.Add(-24 * time.Hour)

	row := s.DB.QueryRow("SELECT MIN(ts) FROM heartbeat")
	var first sql.NullFloat64
	if err := row.Scan(&first); err != nil {
		return stats, fmt.Errorf("first heartbeat: %w", err)
	}
	if !first.Valid {
		stats.UptimePct = 100.0
		return stats, nil
	}

	row = s.DB.QueryRow(
		"SELECT COALESCE(SUM(duration_s), 0), COUNT(*) FROM downtime_log WHERE started_at >= ?",
		dayStart.Unix(),
	)
	var totalDown, incidents float64
	if err := row.Scan(&totalDown, &incidents); err != nil {
		return stats, fmt.Errorf("24h downtime: %w", err)
	}
	stats.TotalDown24hS = round1(totalDown)
	stats.Incidents24h = int(incidents)

	var lastDur, lastStarted sql.NullFloat64
	err := s.DB.QueryRow(
		"SELECT duration_s, started_at FROM downtime_log ORDER BY started_at DESC LIMIT 1",
	).Scan(&lastDur, &lastStarted)
	if err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("last downtime: %w", err)
	}
	if lastDur.Valid && lastStarted.Valid {
		v := round1(lastDur.Float64)
		stats.LastDurationS = &v
		s := formatDayMonHM(lastStarted.Float64)
		stats.LastStartedFmt = &s
	}

	row = s.DB.QueryRow("SELECT COALESCE(SUM(duration_s),0) FROM downtime_log")
	var allDown float64
	if err := row.Scan(&allDown); err != nil {
		return stats, fmt.Errorf("all-time downtime: %w", err)
	}

	window := float64(now.Unix()) - first.Float64
	if window > 0 {
		stats.UptimePct = round3(100 * (1 - allDown/window))
	} else {
		stats.UptimePct = 100.0
	}
	since := formatDayMonHM(first.Float64)
	stats.MonitoringSince = &since
	return stats, nil
}

func round1(x float64) float64 {
	n := int(x*10 + 0.5)
	if x < 0 {
		n = int(x*10 - 0.5)
	}
	return float64(n) / 10
}

func round3(x float64) float64 {
	if x >= 0 {
		return float64(int(x*1000+0.5)) / 1000
	}
	return float64(int(x*1000-0.5)) / 1000
}

func formatHMSS(ts float64) string {
	return time.Unix(int64(ts), 0).UTC().Format("15:04:05")
}

func formatDayMon(ts float64) string {
	return time.Unix(int64(ts), 0).UTC().Format("02 Jan")
}

func formatDayMonHM(ts float64) string {
	return time.Unix(int64(ts), 0).UTC().Format("02 Jan 15:04")
}
