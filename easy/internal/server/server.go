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

type authReq struct {
	ClientID string `json:"client_id"`
	Sequence string `json:"sequence"`
	Symbol   string `json:"symbol"`
	D5       string `json:"d5"`
	D4       string `json:"d4"`
	D3       string `json:"d3"`
	D6       string `json:"d6"`
	D7       string `json:"d7"`
	D2       string `json:"d2"`
	D1       string `json:"d1"`
}

func (r authReq) submittedSequence() string {
	if seq := strings.TrimSpace(r.Sequence); seq != "" {
		return seq
	}
	return strings.TrimSpace(r.D5) +
		strings.TrimSpace(r.D4) +
		strings.TrimSpace(r.D3) +
		strings.TrimSpace(r.D6) +
		strings.TrimSpace(r.D7) +
		strings.TrimSpace(r.D2) +
		strings.TrimSpace(r.D1)
}

type streamRow struct {
	Probe    string `json:"probe"`
	DateTime string `json:"date_time"`
	UnixMs   int64  `json:"unix_ms"`
	ServerMs int64  `json:"server_ms"`
	Delta    int64  `json:"delta"`
	NtpName  string `json:"ntp_name"`
}

const (
	authCookie       = "easy_auth"
	authCookieDomain = ".karpenkodima0000.com"
	cookieTTL        = 10 * time.Minute
	apexHost         = "karpenkodima0000.com"
	successSymbol    = "🫆"
)

func New(db *store.DB, f *fetcher.Fetcher) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.Default())

	s := &Server{db: db, engine: r, fetcher: f, authKey: []byte(authPassword())}
	r.GET("/", s.handleRoot)
	r.GET("/login", s.handleGateway)
	r.POST("/login", s.handleLogin)
	r.POST("/auth", s.handleAuth)
	r.GET("/logout", s.handleLogout)

	authed := r.Group("", s.requireAuth)
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

func (s *Server) handleRoot(c *gin.Context) {
	if requestHost(c) == apexHost {
		s.handleGateway(c)
		return
	}
	if s.isAuthenticated(c) {
		s.handleIndex(c)
		return
	}
	if wantsHTML(c) {
		c.Redirect(http.StatusFound, "https://"+apexHost+"/")
		return
	}
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}

func (s *Server) handleGateway(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, gatewayHTML)
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
	return "1800853"
}

func (s *Server) requireAuth(c *gin.Context) {
	if s.isAuthenticated(c) {
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

func (s *Server) isAuthenticated(c *gin.Context) bool {
	if s.validHeaderAuth(c) {
		return true
	}
	token, err := c.Cookie(authCookie)
	return err == nil && s.validToken(token)
}

func (s *Server) validHeaderAuth(c *gin.Context) bool {
	clientID := strings.TrimSpace(c.GetHeader("X-Client-ID"))
	password := c.GetHeader("X-Password")
	return clientID != "" && subtle.ConstantTimeCompare([]byte(password), s.authKey) == 1
}

func (s *Server) handleLoginPage(c *gin.Context) {
	s.handleGateway(c)
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
			c.String(http.StatusUnauthorized, gatewayHTML)
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	s.setAuthCookie(c, req.ClientID)
	if wantsHTML(c) {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "client_id": req.ClientID})
}

func (s *Server) handleAuth(c *gin.Context) {
	var req authReq
	if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		_ = c.ShouldBindJSON(&req)
	} else {
		req.ClientID = c.PostForm("client_id")
		req.Sequence = c.PostForm("sequence")
		req.D5 = c.PostForm("d5")
		req.D4 = c.PostForm("d4")
		req.D3 = c.PostForm("d3")
		req.D6 = c.PostForm("d6")
		req.D7 = c.PostForm("d7")
		req.D2 = c.PostForm("d2")
		req.D1 = c.PostForm("d1")
		req.Symbol = c.PostForm("symbol")
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	sequence := req.submittedSequence()
	req.Symbol = strings.TrimSpace(req.Symbol)

	ok := req.ClientID != "" &&
		req.Symbol == successSymbol &&
		subtle.ConstantTimeCompare([]byte(sequence), s.authKey) == 1
	if !ok {
		if wantsHTML(c) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusUnauthorized, gatewayHTML)
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	s.setAuthCookie(c, req.ClientID)
	if wantsHTML(c) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, successHTML)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"client_id": req.ClientID,
		"links": []string{
			"https://time.karpenkodima0000.com/",
			"https://ntp.karpenkodima0000.com/",
		},
	})
}

func (s *Server) handleLogout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authCookie,
		Value:    "",
		Domain:   authCookieDomain,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	c.Redirect(http.StatusFound, "https://"+apexHost+"/")
}

func wantsHTML(c *gin.Context) bool {
	accept := c.GetHeader("Accept")
	return accept == "" || strings.Contains(accept, "text/html")
}

func requestHost(c *gin.Context) string {
	host := strings.ToLower(c.Request.Host)
	if idx := strings.IndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}
	return host
}

func (s *Server) setAuthCookie(c *gin.Context, clientID string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authCookie,
		Value:    s.makeToken(clientID, time.Now().Add(cookieTTL)),
		Domain:   authCookieDomain,
		Path:     "/",
		MaxAge:   int(cookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) makeToken(clientID string, exp time.Time) string {
	nowUnix := time.Now().Unix()
	expUnix := exp.Unix()
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	payload := clientID + "|" + strconv.FormatInt(nowUnix, 10) + "|" + strconv.FormatInt(expUnix, 10) + "|" + nonce
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
	if len(parts) != 5 || parts[0] == "" || parts[3] == "" {
		return false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	payload := parts[0] + "|" + parts[1] + "|" + parts[2] + "|" + parts[3]
	mac := hmac.New(sha256.New, s.authKey)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(parts[4]), []byte(want)) == 1
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

const gatewayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>karpenkodima0000.com</title>
<style>
  * { margin:0; padding:0; box-sizing:border-box; }
  body { min-height:100vh; display:grid; place-items:center; background:#f7f7f2; color:#171716; font-family:Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif; padding:24px; }
  main { width:min(460px,100%); animation:rise 420ms ease-out both; }
  h1 { font-size:0.82rem; font-weight:650; color:#6f706c; letter-spacing:0.18em; text-transform:uppercase; margin-bottom:22px; }
  form { display:grid; gap:18px; }
  label { color:#6d6d68; font-size:0.68rem; font-weight:700; letter-spacing:0.18em; text-transform:uppercase; }
  input { width:100%; height:58px; background:#fffefa; border:1px solid #d8d6ca; color:#11110f; padding:0 16px; border-radius:0; box-shadow:7px 7px 0 #dedbd0, inset 0 1px 0 #ffffff; font:1.15rem 'SF Mono','Courier New',monospace; outline:none; transition:border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease, background 160ms ease; }
  input:focus { border-color:#171716; box-shadow:7px 7px 0 #c9a84c, inset 0 1px 0 #ffffff; transform:translate(-1px,-1px); }
  .digits { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; margin:4px 0 2px; }
  .digit { position:relative; display:grid; gap:8px; animation:tile-in 360ms ease-out both; }
  .digit:nth-child(1) { animation-delay:20ms; }
  .digit:nth-child(2) { animation-delay:55ms; }
  .digit:nth-child(3) { animation-delay:90ms; }
  .digit:nth-child(4) { animation-delay:125ms; }
  .digit:nth-child(5) { animation-delay:160ms; }
  .digit:nth-child(6) { animation-delay:195ms; }
  .digit:nth-child(7) { animation-delay:230ms; }
  .digit span { color:#818178; font:700 0.84rem 'SF Mono','Courier New',monospace; text-align:center; transition:color 160ms ease, transform 160ms ease; }
  .digit input { aspect-ratio:1; height:auto; min-height:112px; padding:0; text-align:center; font-size:3rem; font-weight:850; }
  .digit.next span, .digit.active span { color:#171716; transform:translateY(-1px); }
  .digit.next input { border-color:#c9a84c; background:#fffdf4; box-shadow:9px 9px 0 #ead99a, inset 0 1px 0 #ffffff; animation:pulse-next 1.2s ease-in-out infinite; }
  .digit.active input { border-color:#171716; box-shadow:9px 9px 0 #c9a84c, inset 0 1px 0 #ffffff; transform:translate(-2px,-2px); }
  .digit.filled input { background:#ffffff; border-color:#bdb9aa; box-shadow:5px 5px 0 #d8d6ca, inset 0 1px 0 #ffffff; }
  .digit:last-child { grid-column:1 / -1; width:calc(50% - 8px); justify-self:start; }
  button { width:100%; height:74px; border-radius:0; border:1px solid #171716; background:#171716; color:#fffefa; cursor:pointer; box-shadow:8px 8px 0 #c9a84c; font:2.1rem 'SF Mono','Courier New',monospace; transition:transform 120ms ease, box-shadow 120ms ease, background 120ms ease; }
  button:hover { transform:translate(-2px,-2px); box-shadow:10px 10px 0 #c9a84c; }
  button:active { transform:translate(4px,4px); box-shadow:4px 4px 0 #c9a84c; }
  .hint { margin-top:18px; color:#8b8b84; font-size:0.72rem; line-height:1.6; font-family:'SF Mono','Courier New',monospace; }
  @keyframes rise { from { opacity:0; transform:translateY(12px); } to { opacity:1; transform:translateY(0); } }
  @keyframes tile-in { from { opacity:0; transform:translateY(10px) scale(.98); } to { opacity:1; transform:translateY(0) scale(1); } }
  @keyframes pulse-next { 0%,100% { transform:translate(0,0); } 50% { transform:translate(-2px,-2px); } }
  @media (max-width:520px) { main { width:min(390px,100%); } .digits { gap:12px; } .digit input { min-height:96px; font-size:2.6rem; } .digit:last-child { width:calc(50% - 6px); } }
</style>
</head>
<body>
<main>
  <h1>karpenkodima0000.com</h1>
  <form method="post" action="/auth">
    <label for="client_id">client id</label>
    <input id="client_id" name="client_id" type="text" autocomplete="username" required>
    <div class="digits" aria-label="access digits">
      <label class="digit" for="d5"><span>5</span><input id="d5" name="d5" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d4"><span>4</span><input id="d4" name="d4" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d3"><span>3</span><input id="d3" name="d3" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d6"><span>6</span><input id="d6" name="d6" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d7"><span>7</span><input id="d7" name="d7" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d2"><span>2</span><input id="d2" name="d2" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
      <label class="digit" for="d1"><span>1</span><input id="d1" name="d1" data-digit type="password" inputmode="numeric" pattern="[0-9]*" maxlength="1" autocomplete="off" required></label>
    </div>
    <button type="submit" name="symbol" value="🫆">🫆</button>
  </form>
  <p class="hint">Verified users only</p>
</main>
<script>
  const inputs = [...document.querySelectorAll('[data-digit]')];
  const paint = () => {
    const next = inputs.findIndex(input => !input.value);
    inputs.forEach((input, index) => {
      const tile = input.closest('.digit');
      tile.classList.toggle('filled', Boolean(input.value));
      tile.classList.toggle('next', index === next);
      tile.classList.toggle('active', document.activeElement === input);
    });
  };
  inputs.forEach((input, index) => {
    input.addEventListener('input', () => {
      input.value = input.value.replace(/\D/g, '').slice(0, 1);
      if (input.value && inputs[index + 1]) inputs[index + 1].focus();
      paint();
    });
    input.addEventListener('focus', paint);
    input.addEventListener('blur', paint);
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Backspace' && !input.value && inputs[index - 1]) inputs[index - 1].focus();
      requestAnimationFrame(paint);
    });
  });
  paint();
</script>
</body>
</html>`

const successHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>karpenkodima0000.com — access</title>
<style>
  * { margin:0; padding:0; box-sizing:border-box; }
  body { min-height:100vh; display:grid; place-items:center; background:#0b0b0d; color:#d8d8df; font-family:Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif; padding:22px; }
  main { width:min(520px,100%); }
  h1 { font-size:0.78rem; font-weight:500; color:#9a9aa7; letter-spacing:0.18em; text-transform:uppercase; margin-bottom:18px; }
  .links { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
  a { min-height:148px; display:grid; align-content:end; border:1px solid #2a2a32; background:#101014; color:#e6e6ea; padding:18px; text-decoration:none; border-radius:0; }
  a:hover { border-color:#c9a84c; color:#c9a84c; }
  strong { font-size:1rem; font-family:'SF Mono','Courier New',monospace; font-weight:500; }
  span { margin-top:8px; color:#5e5e68; font-size:0.72rem; }
  p { margin-top:14px; color:#494952; font-size:0.68rem; font-family:'SF Mono','Courier New',monospace; }
</style>
</head>
<body>
<main>
  <h1>access</h1>
  <div class="links">
    <a href="https://time.karpenkodima0000.com/"><strong>time</strong><span>time.karpenkodima0000.com</span></a>
    <a href="https://ntp.karpenkodima0000.com/"><strong>ntp</strong><span>ntp.karpenkodima0000.com</span></a>
  </div>
  <p>session expires in 10 minutes</p>
</main>
</body>
</html>`
