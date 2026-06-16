// Package server: HTTP-обробники NTP-дашборду на базі Gin.
package server

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/karpenkodima/ntp-dashboard/internal/metrics"
	"github.com/karpenkodima/ntp-dashboard/internal/sampler"
	"github.com/karpenkodima/ntp-dashboard/internal/store"
)

// Deps — залежності HTTP-сервера.
type Deps struct {
	Sampler *sampler.Sampler
	Store   *store.Store
	DBPath  string
	Bus     *SSEBus
}

// NewRouter збирає всі маршрути в єдиний *gin.Engine.
func NewRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:    []string{"Content-Type"},
	}))

	// dashboard.html вбудовуємо як asset
	r.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		_, _ = c.Writer.Write(dashboardHTML)
	})

	r.GET("/ntp/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, d.Sampler.Status())
	})

	r.GET("/ntp/recent", func(c *gin.Context) {
		n := intQuery(c, "n", 50, 200)
		samples, err := d.Store.RecentSamples(n)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"samples": samples})
	})

	r.GET("/ntp/servers", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"servers": d.Sampler.Servers()})
	})

	r.GET("/ntp/downtime", func(c *gin.Context) {
		n := intQuery(c, "n", 20, 100)
		events, err := d.Store.RecentDowntime(n)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": events})
	})

	r.GET("/ntp/uptime-stats", func(c *gin.Context) {
		stats, err := d.Store.ComputeUptimeStats(time.Now())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, stats)
	})

	r.GET("/ntp/deploys", func(c *gin.Context) {
		n := intQuery(c, "n", 20, 100)
		deploys, err := d.Store.RecentDeploys(n)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deploys": deploys})
	})

	r.GET("/ntp/server-time", func(c *gin.Context) {
		now := time.Now().UTC()
		c.JSON(http.StatusOK, gin.H{
			"utc":     now.Format("2006-01-02 15:04:05"),
			"ts":      now.Unix(),
			"iso":     now.Format(time.RFC3339Nano),
			"fetched": now.Format("15:04:05") + " UTC",
		})
	})

	r.POST("/ntp/deploy", func(c *gin.Context) {
		var payload struct {
			DurationMs *int    `json:"duration_ms"`
			GitHash    string  `json:"git_hash"`
			Message    string  `json:"message"`
		}
		_ = c.ShouldBindJSON(&payload)
		hash := payload.GitHash
		msg := payload.Message
		if err := d.Sampler.LogDeploy(payload.DurationMs, hash, msg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		now := time.Now().UTC()
		deployedAt := float64(now.Unix())
		c.JSON(http.StatusOK, gin.H{
			"deployed_at": deployedAt,
			"ts_fmt":      now.Format("02 Jan 15:04:05"),
			"duration_ms": payload.DurationMs,
			"git_hash":    hash,
			"message":     msg,
		})
	})

	r.POST("/auth/verify", func(c *gin.Context) {
		var payload struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&payload)
		c.JSON(http.StatusOK, gin.H{"ok": AuthOK(payload.Password)})
	})

	r.POST("/ntp/db/clear", func(c *gin.Context) {
		var payload struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&payload)
		if !AuthOK(payload.Password) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if err := d.Sampler.ClearSamples(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "cleared"})
	})

	r.GET("/ntp/db/export", func(c *gin.Context) {
		password := c.Query("password")
		if !AuthOK(password) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		tmpDB, tmpGZ, err := exportDB(d.DBPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// очищення тимчасового файлу після відправлення
		c.Header("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", gzFilename()))
		c.Header("Content-Type", "application/gzip")
		defer func() {
			_ = os.Remove(tmpGZ)
		}()
		c.File(tmpGZ)
	})

	// ── SSE streams ──────────────────────────────────────────────────────────

	r.GET("/events/ntp", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")

		id, ch := d.Bus.Subscribe(64)
		defer d.Bus.Unsubscribe(id)

		// flush helper
		flusher, _ := c.Writer.(http.Flusher)

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case sample := <-ch:
				writeSSE(c, "data", sample)
				if flusher != nil {
					flusher.Flush()
				}
			case <-time.After(3 * time.Second):
				ping := map[string]any{"ping": true}
				ping["running"] = d.Sampler.Status().Running
				ping["next_in"] = d.Sampler.Status().NextIn
				writeSSE(c, "data", ping)
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})

	r.GET("/events/metrics", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		flusher, _ := c.Writer.(http.Flusher)

		for {
			select {
			case <-c.Request.Context().Done():
				return
			default:
			}
			payload := metrics.Collect()
			writeSSE(c, "data", payload)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(500 * time.Millisecond)
		}
	})

	return r
}

// publishLoop читає канал SSEBus і публікує у підписників (викликається в main).
// Тут тримаємо лише як публічну точку для main, який має запустити цей loop.
//
// На практиці sampler.publish викликається напряму з внутрішнього циклу;
// ця функція потрібна лише якщо ми хочемо ізолювати шину від sampler (наразі ні).
func PublishLoop(_ *sampler.Sampler, _ *SSEBus) {
	// no-op: див. sampler.publish
}

// helpers

func intQuery(c *gin.Context, key string, def, max int) int {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n > max {
		return max
	}
	if n <= 0 {
		return def
	}
	return n
}

func writeSSE(c *gin.Context, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.Writer.WriteString(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
}

func gzFilename() string {
	return fmt.Sprintf("ntp_%s.db.gz", time.Now().UTC().Format("20060102_150405"))
}

// exportDB робить SQLite-backup у тимчасовий файл і gzip-архів.
func exportDB(srcPath string) (tmpDB, tmpGZ string, err error) {
	tmpDir, err := os.MkdirTemp("", "ntp-export-*")
	if err != nil {
		return "", "", err
	}
	tmpDB = filepath.Join(tmpDir, "ntp.db")
	tmpGZ = tmpDB + ".gz"

	if err := backupDB(srcPath, tmpDB); err != nil {
		return "", "", err
	}

	in, err := os.Open(tmpDB)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	out, err := os.Create(tmpGZ)
	if err != nil {
		return "", "", err
	}
	defer out.Close()
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		return "", "", err
	}
	if err := zw.Close(); err != nil {
		return "", "", err
	}
	_ = os.Remove(tmpDB)
	return tmpDB, tmpGZ, nil
}

// backupDB робить атомарний знімок SQLite через VACUUM INTO у тимчасовий файл.
// Це pure-Go рішення, що не потребує CGO і повністю підтримується modernc.org/sqlite.
func backupDB(srcPath, dstPath string) error {
	conn, err := sql.Open("sqlite", "file:"+srcPath+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return err
	}
	defer conn.Close()
	// екранування лапок для VACUUM INTO
	escaped := strings.ReplaceAll(dstPath, "'", "''")
	_, err = conn.Exec("VACUUM INTO '" + escaped + "'")
	return err
}

// dashboardHTML заповнюється з main при ініціалізації через SetDashboard.
// Ми тримаємо його як package-level змінну, щоб уникнути зайвої залежності в server.
var dashboardHTML []byte

// SetDashboard встановлює embed-вміст dashboard.html.
func SetDashboard(b []byte) { dashboardHTML = b }
