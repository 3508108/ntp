package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
}

type intervalReq struct {
	Interval string `json:"interval"`
}

func New(db *store.DB, f *fetcher.Fetcher) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.Default())

	s := &Server{db: db, engine: r, fetcher: f}
	r.GET("/", s.handleIndex)
	r.GET("/api/recent", s.handleRecent)
	r.GET("/api/stream", s.handleStream)
	r.POST("/0000", s.handle0000)
	r.GET("/api/interval", s.handleGetInterval)
	r.POST("/api/interval", s.handleSetInterval)
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

	c.Stream(func(w io.Writer) bool {
		rows, err := s.db.Recent(1)
		if err == nil && len(rows) > 0 {
			r := rows[0]
			fmt.Fprintf(w, "data: {\"probe\":\"%s\",\"date_time\":\"%s\",\"unix_ms\":%d,\"server_ms\":%d,\"delta\":%d,\"ntp_name\":\"%s\"}\n\n",
				r.Probe, r.DateTime, r.UnixMs, r.ServerMs, r.UnixMs-r.ServerMs, r.NtpName)
			flusher.Flush()
		}
		time.Sleep(500 * time.Millisecond)
		return true
	})
}

type ping0000 struct {
	Time      string `json:"time"`
	Timestamp string `json:"timestamp"`
	Device    string `json:"device"`
	Action    string `json:"action"`
}

func (s *Server) handle0000(c *gin.Context) {
	var p ping0000
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.db.InsertPing0000(p.Time, p.Timestamp, p.Device, p.Action); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
  .controls { display:flex; gap:8px; margin-bottom:12px; align-items:center; }
  .btn { background:#16161e; border:1px solid #2a2a38; color:#707088; padding:4px 12px; border-radius:4px; font-family:'Courier New',monospace; font-size:0.62rem; letter-spacing:2px; cursor:pointer; transition:all 0.15s; }
  .btn:hover { border-color:#c9a84c66; color:#c9a84c; }
  input { background:#0e0e12; border:1px solid #2a2a38; color:#c8c8d4; font-family:'Courier New',monospace; font-size:0.72rem; padding:4px 8px; border-radius:4px; width:80px; text-align:center; }
  input:focus { border-color:#c9a84c55; outline:none; }
</style>
</head>
<body>
<h1>⊙ ntp/easy · time probes</h1>
<div class="controls">
  <span class="meta">interval:</span>
  <input id="interval-input" type="text" value="10s" onkeydown="if(event.key==='Enter')setInterval()">
  <button class="btn" onclick="setInterval()">set</button>
  <span class="meta" id="interval-status">current: 10s</span>
</div>
<table id="tbl">
  <thead>
    <tr><th>probe</th><th>date-time</th><th>unix ms</th><th>server ms</th><th>delta</th><th>ntp name</th></tr>
  </thead>
  <tbody id="tbody"></tbody>
</table>
<div class="meta">1 cycle = 3 NTP (apple/google/nist) · SSE /api/stream</div>

<script>
  const tbody = document.getElementById('tbody');
  let rows = [];

  async function load() {
    const r = await fetch('/api/recent?n=100');
    const d = await r.json();
    rows = d.rows || [];
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
    await fetch('/api/interval', {method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({interval: val})});
    getInterval();
  }

  function render() {
    tbody.innerHTML = rows.slice(0,100).map(r => {
      const d = r.unix_ms - r.server_ms;
      const c = Math.abs(d) < 100 ? 'ok' : Math.abs(d) < 500 ? 'warn' : 'err';
      return '<tr>' +
        '<td class="probe">' + r.probe + '</td>' +
        '<td>' + r.date_time + '</td>' +
        '<td>' + r.unix_ms + '</td>' +
        '<td class="' + c + '">' + (r.server_ms || '—') + '</td>' +
        '<td class="' + c + '">' + (r.server_ms ? (d>0?'+':'') + d : '—') + '</td>' +
        '<td>' + (r.ntp_name || '—') + '</td>' +
      '</tr>';
    }).join('');
  }

  const es = new EventSource('/api/stream');
  es.onmessage = ev => { const d=JSON.parse(ev.data); rows.unshift(d); if(rows.length>200)rows.pop(); render(); };

  load();
  getInterval();
  setInterval(getInterval, 30000);
</script>
</body>
</html>`
