package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"ntp/easy/internal/fetcher"
	"ntp/easy/internal/store"
)

type Server struct {
	db      *store.DB
	engine  *gin.Engine
	srv     *http.Server
	fetcher *fetcher.Fetcher
	authKey []byte
}

type intervalReq struct {
	Interval string `json:"interval"`
}

type loginReq struct {
	ClientID string `json:"client_id"`
	Password string `json:"password"`
}

type streamRow struct {
	Probe    string `json:"probe"`
	DateTime string `json:"date_time"`
	UnixMs   int64  `json:"unix_ms"`
	ServerMs int64  `json:"server_ms"`
	Delta    int64  `json:"delta"`
	NtpName  string `json:"ntp_name"`
}

const authCookie = "easy_auth"

func New(db *store.DB, f *fetcher.Fetcher) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.Default())

	s := &Server{db: db, engine: r, fetcher: f, authKey: []byte(authPassword())}
	r.GET("/login", s.handleLoginPage)
	r.POST("/login", s.handleLogin)
	r.GET("/logout", s.handleLogout)

	authed := r.Group("", s.requireAuth)
	authed.GET("/", s.handleIndex)
	authed.GET("/api/recent", s.handleRecent)
	authed.GET("/api/logs", s.handleLogs)
	authed.GET("/api/stream", s.handleStream)
	authed.GET("/api/interval", s.handleGetInterval)
	authed.POST("/api/interval", s.handleSetInterval)
	return s
}

func (s *Server) Run(addr string) error {
	s.srv = &http.Server{Addr: addr, Handler: s.engine}
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleIndex(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, indexHTML)
}

func (s *Server) handleRecent(c *gin.Context) {
	rows, err := s.db.Recent(500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

func (s *Server) handleLogs(c *gin.Context) {
	rng := strings.ToLower(strings.TrimSpace(c.DefaultQuery("range", "hour")))
	since := int64(0)
	switch rng {
	case "hour", "1h":
		since = time.Now().Add(-time.Hour).UnixMilli()
		rng = "hour"
	case "day", "24h":
		since = time.Now().Add(-24 * time.Hour).UnixMilli()
		rng = "day"
	case "week", "7d":
		since = time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
		rng = "week"
	case "all", "":
		rng = "all"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid range"})
		return
	}

	rows, err := s.db.LogsSince(since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"range": rng, "count": len(rows), "rows": rows})
}

func (s *Server) handleStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}

	var lastID int64
	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		default:
		}

		rows, err := s.db.Recent(1)
		if err == nil && len(rows) > 0 && rows[0].ID != lastID {
			r := rows[0]
			lastID = r.ID
			payload, err := json.Marshal(streamRow{
				Probe:    r.Probe,
				DateTime: r.DateTime,
				UnixMs:   r.UnixMs,
				ServerMs: r.ServerMs,
				Delta:    r.UnixMs - r.ServerMs,
				NtpName:  r.NtpName,
			})
			if err == nil {
				w.Write([]byte("data: "))
				w.Write(payload)
				w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
		select {
		case <-c.Request.Context().Done():
			return false
		case <-time.After(500 * time.Millisecond):
			return true
		}
	})
}

func (s *Server) handleGetInterval(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"interval": s.fetcher.Interval().String()})
}

func (s *Server) handleSetInterval(c *gin.Context) {
	var req intervalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	d, err := time.ParseDuration(req.Interval)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid duration"})
		return
	}
	s.fetcher.SetInterval(d)
	c.JSON(http.StatusOK, gin.H{"interval": d.String()})
}

func authPassword() string {
	if p := os.Getenv("EASY_PASSWORD"); p != "" {
		return p
	}
	return "350810818"
}

func (s *Server) requireAuth(c *gin.Context) {
	if s.validHeaderAuth(c) {
		c.Next()
		return
	}
	if token, err := c.Cookie(authCookie); err == nil && s.validToken(token) {
		c.Next()
		return
	}
	if wantsHTML(c) {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}

func (s *Server) validHeaderAuth(c *gin.Context) bool {
	clientID := strings.TrimSpace(c.GetHeader("X-Client-ID"))
	password := c.GetHeader("X-Password")
	return clientID != "" && subtle.ConstantTimeCompare([]byte(password), s.authKey) == 1
}

func (s *Server) handleLoginPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, loginHTML)
}

func (s *Server) handleLogin(c *gin.Context) {
	var req loginReq
	if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		_ = c.ShouldBindJSON(&req)
	} else {
		req.ClientID = c.PostForm("client_id")
		req.Password = c.PostForm("password")
	}
	req.ClientID = strings.TrimSpace(req.ClientID)

	if req.ClientID == "" || subtle.ConstantTimeCompare([]byte(req.Password), s.authKey) != 1 {
		if wantsHTML(c) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusUnauthorized, loginHTML)
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authCookie,
		Value:    s.makeToken(req.ClientID, time.Now().Add(7*24*time.Hour)),
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	if wantsHTML(c) {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "client_id": req.ClientID})
}

func (s *Server) handleLogout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: authCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	c.Redirect(http.StatusFound, "/login")
}

func wantsHTML(c *gin.Context) bool {
	accept := c.GetHeader("Accept")
	return accept == "" || strings.Contains(accept, "text/html")
}

func (s *Server) makeToken(clientID string, exp time.Time) string {
	expUnix := exp.Unix()
	payload := clientID + "|" + strconv.FormatInt(expUnix, 10)
	mac := hmac.New(sha256.New, s.authKey)
	mac.Write([]byte(payload))
	raw := payload + "|" + hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (s *Server) validToken(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] == "" {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	payload := parts[0] + "|" + parts[1]
	mac := hmac.New(sha256.New, s.authKey)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(parts[2]), []byte(want)) == 1
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ntp/easy — time probes</title>
<style>
  * { margin:0; padding:0; box-sizing:border-box; }
  body { background:#0e0e12; color:#c8c8d4; font-family:'Courier New',monospace; padding:20px 24px; }
  h1 { font-size:0.85rem; color:#c9a84c; letter-spacing:3px; text-transform:uppercase; margin-bottom:16px; }
  table { width:100%; border-collapse:collapse; font-size:0.72rem; }
  th { text-align:left; padding:6px 10px; color:#5a5a70; text-transform:uppercase; letter-spacing:2px; font-size:0.58rem; border-bottom:1px solid #24242e; }
  td { padding:5px 10px; border-bottom:1px solid #1a1a24; font-variant-numeric:tabular-nums; }
  tr:hover { background:#1a1a24; }
  .ok { color:#6fcf97; }
  .warn { color:#c9a84c; }
  .err { color:#eb5757; }
  .probe { color:#56a4f5; font-weight:bold; }
  .meta { font-size:0.58rem; color:#404055; margin-top:12px; letter-spacing:2px; }
  .controls { display:flex; gap:8px; margin-bottom:12px; align-items:center; flex-wrap:wrap; }
  .btn { background:#16161e; border:1px solid #2a2a38; color:#707088; padding:4px 12px; border-radius:4px; font-family:'Courier New',monospace; font-size:0.62rem; letter-spacing:2px; cursor:pointer; transition:all 0.15s; }
  .btn:hover { border-color:#c9a84c66; color:#c9a84c; }
  .btn.active { border-color:#c9a84c; color:#c9a84c; }
  input { background:#0e0e12; border:1px solid #2a2a38; color:#c8c8d4; font-family:'Courier New',monospace; font-size:0.72rem; padding:4px 8px; border-radius:4px; width:80px; text-align:center; }
  input:focus { border-color:#c9a84c55; outline:none; }
  .spacer { flex:1; }
  a { color:#707088; text-decoration:none; }
  a:hover { color:#c9a84c; }
</style>
</head>
<body>
<h1>⊙ ntp/easy · time probes</h1>
<div class="controls">
  <span class="meta">interval:</span>
  <input id="interval-input" type="text" value="10s" onkeydown="if(event.key==='Enter')setInterval()">
  <button class="btn" onclick="setInterval()">set</button>
  <span class="meta" id="interval-status">current: 10s</span>
  <span class="spacer"></span>
  <button class="btn active" data-range="hour" onclick="setRange('hour')">hour</button>
  <button class="btn" data-range="day" onclick="setRange('day')">day</button>
  <button class="btn" data-range="week" onclick="setRange('week')">week</button>
  <button class="btn" data-range="all" onclick="setRange('all')">all</button>
  <a class="meta" href="/logout">logout</a>
</div>
<table id="tbl">
  <thead>
    <tr><th>probe</th><th>date-time</th><th>unix ms</th><th>server ms</th><th>delta</th><th>ntp name</th></tr>
  </thead>
  <tbody id="tbody"></tbody>
</table>
<div class="meta" id="log-meta">1 cycle = 3 NTP (apple/google/nist) · full logs sorted by created_at desc · SSE /api/stream</div>

<script>
  const tbody = document.getElementById('tbody');
  let rows = [];
  let activeRange = 'hour';

  async function load() {
    const r = await fetch('/api/logs?range=' + encodeURIComponent(activeRange));
    if (r.status === 401) { location.href='/login'; return; }
    const d = await r.json();
    rows = d.rows || [];
    document.getElementById('log-meta').textContent = 'range: ' + d.range + ' · rows: ' + d.count + ' · sorted by created_at desc · stored fully in sqlite';
    render();
  }

  async function getInterval() {
    const r = await fetch('/api/interval');
    const d = await r.json();
    document.getElementById('interval-status').textContent = 'current: ' + d.interval;
    document.getElementById('interval-input').value = d.interval;
  }

  async function setInterval() {
    const val = document.getElementById('interval-input').value;
    const r = await fetch('/api/interval', {method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({interval: val})});
    if (r.status === 401) { location.href='/login'; return; }
    getInterval();
  }

  function setRange(r) {
    activeRange = r;
    document.querySelectorAll('[data-range]').forEach(b => b.classList.toggle('active', b.dataset.range === r));
    load();
  }

  function esc(v) {
    return String(v ?? '').replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]));
  }

  function render() {
    tbody.innerHTML = rows.map(r => {
      const d = r.unix_ms - r.server_ms;
      const c = Math.abs(d) < 100 ? 'ok' : Math.abs(d) < 500 ? 'warn' : 'err';
      return '<tr>' +
        '<td class="probe">' + esc(r.probe) + '</td>' +
        '<td>' + esc(r.date_time) + '</td>' +
        '<td>' + r.unix_ms + '</td>' +
        '<td class="' + c + '">' + (r.server_ms || '—') + '</td>' +
        '<td class="' + c + '">' + (r.server_ms ? (d>0?'+':'') + d : '—') + '</td>' +
        '<td>' + esc(r.ntp_name || '—') + '</td>' +
      '</tr>';
    }).join('');
  }

  const es = new EventSource('/api/stream');
  es.onmessage = ev => {
    const d=JSON.parse(ev.data);
    if(activeRange !== 'all') rows.unshift(d);
    render();
  };

  load();
  getInterval();
  setInterval(getInterval, 30000);
</script>
</body>
</html>`

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ntp/easy — login</title>
<style>
  * { margin:0; padding:0; box-sizing:border-box; }
  body { min-height:100vh; display:grid; place-items:center; background:#0e0e12; color:#c8c8d4; font-family:'Courier New',monospace; padding:20px; }
  form { width:min(360px,100%); border:1px solid #24242e; padding:24px; background:#111118; }
  h1 { font-size:0.85rem; color:#c9a84c; letter-spacing:3px; text-transform:uppercase; margin-bottom:18px; }
  label { display:block; color:#707088; font-size:0.62rem; letter-spacing:2px; text-transform:uppercase; margin:14px 0 6px; }
  input { width:100%; background:#0e0e12; border:1px solid #2a2a38; color:#c8c8d4; font-family:'Courier New',monospace; padding:9px 10px; border-radius:4px; }
  button { width:100%; margin-top:18px; background:#16161e; border:1px solid #c9a84c66; color:#c9a84c; padding:10px 12px; border-radius:4px; font-family:'Courier New',monospace; letter-spacing:2px; cursor:pointer; }
</style>
</head>
<body>
<form method="post" action="/login">
  <h1>ntp/easy auth</h1>
  <label for="client_id">client id</label>
  <input id="client_id" name="client_id" type="text" autocomplete="username" required>
  <label for="password">password</label>
  <input id="password" name="password" type="password" autocomplete="current-password" required>
  <button type="submit">login</button>
</form>
</body>
</html>`
