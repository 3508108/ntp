"""
NTP Sampler — picks NIST NTP servers at random (using random.org for true
randomness), queries each for time offset / delay, persists to SQLite.
"""
import ntplib
import re
import sqlite3
import threading
import time
import random
import os
import queue
from datetime import datetime

try:
    import requests as _requests
    _HAS_REQUESTS = True
except ImportError:
    _HAS_REQUESTS = False

NIST_URL    = "https://tf.nist.gov/tf-cgi/servers.cgi"
RANDORG_URL = (
    "https://www.random.org/integers/"
    "?num={n}&min={min}&max={max}&col=1&base=10&format=plain&rnd=new"
)

FALLBACK_SERVERS = [
    "time-a-g.nist.gov",   "time-b-g.nist.gov",   "time-c-g.nist.gov",
    "time-d-g.nist.gov",   "time-e-g.nist.gov",   "time-f-g.nist.gov",
    "time-a-wwv.nist.gov", "time-b-wwv.nist.gov",  "time-c-wwv.nist.gov",
    "time-d-wwv.nist.gov", "time-e-wwv.nist.gov",
    "time-a-b.nist.gov",   "time-b-b.nist.gov",    "time-c-b.nist.gov",
    "time-d-b.nist.gov",   "time-e-b.nist.gov",
    "ntp-b.nist.gov",      "ntp-wwv.nist.gov",     "ntp.nist.gov",
]
_SKIP = ("www.", "ftp.", "mail.", "smtp.")


class NTPSampler:
    """Continuously samples NIST NTP servers at random.org-driven random intervals."""

    def __init__(self, db_path=None, interval_min=30, interval_max=120):
        self._db_path       = db_path or os.environ.get("NTP_DB", "ntp.db")
        self._interval_min  = interval_min
        self._interval_max  = interval_max
        self._lock          = threading.Lock()
        self._running       = False
        self._thread        = None
        self._next_in       = 0
        self._servers       = []
        self._last_sample   = None
        self._total         = 0
        self._queues        = []   # SSE subscriber queues
        self._init_db()
        self._refresh_servers()

    # ── database ───────────────────────────────────────────────────────────────

    def _init_db(self):
        with sqlite3.connect(self._db_path) as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS ntp_samples (
                    id          INTEGER PRIMARY KEY AUTOINCREMENT,
                    server_host TEXT    NOT NULL,
                    offset_ms   REAL,
                    delay_ms    REAL,
                    stratum     INTEGER,
                    rand_idx    INTEGER,
                    next_sec    INTEGER,
                    ok          INTEGER NOT NULL DEFAULT 1,
                    error       TEXT,
                    ts          REAL    NOT NULL
                )
            """)
            conn.execute(
                "CREATE INDEX IF NOT EXISTS idx_ntp_ts ON ntp_samples(ts)"
            )
            row = conn.execute("SELECT COUNT(*) FROM ntp_samples").fetchone()
            self._total = row[0] if row else 0

    # ── server list ────────────────────────────────────────────────────────────

    def _refresh_servers(self):
        """Download the NIST server list; use embedded fallback on failure."""
        if not _HAS_REQUESTS:
            with self._lock:
                self._servers = FALLBACK_SERVERS[:]
            return
        try:
            resp       = _requests.get(NIST_URL, timeout=10)
            candidates = re.findall(
                r"\b([a-z0-9][a-z0-9\-\.]*\.nist\.gov)\b", resp.text
            )
            servers = [
                s for s in dict.fromkeys(candidates)
                if not any(s.startswith(p) for p in _SKIP)
            ]
            if len(servers) >= 5:
                with self._lock:
                    self._servers = servers
                return
        except Exception:
            pass
        with self._lock:
            self._servers = FALLBACK_SERVERS[:]

    # ── true randomness ────────────────────────────────────────────────────────

    def _rand_int(self, lo, hi):
        """Random integer from random.org; falls back to random.randint."""
        if not _HAS_REQUESTS or lo == hi:
            return random.randint(lo, hi)
        try:
            url  = RANDORG_URL.format(n=1, min=lo, max=hi)
            resp = _requests.get(url, timeout=5)
            val  = resp.text.strip()
            if resp.status_code == 200 and val.lstrip("-").isdigit():
                return int(val)
        except Exception:
            pass
        return random.randint(lo, hi)

    # ── single NTP sample ──────────────────────────────────────────────────────

    def _do_sample(self):
        with self._lock:
            servers = self._servers[:]

        rand_idx = self._rand_int(0, len(servers) - 1)
        next_sec = self._rand_int(self._interval_min, self._interval_max)
        server   = servers[rand_idx]
        now      = time.time()

        try:
            resp      = ntplib.NTPClient().request(server, version=3, timeout=5)
            offset_ms = round(resp.offset * 1000, 3)
            delay_ms  = round(resp.delay  * 1000, 3)
            stratum   = resp.stratum
            ok        = True
            error     = None
        except Exception as exc:
            offset_ms = delay_ms = stratum = None
            ok    = False
            error = str(exc)[:120]

        sample = {
            "server":    server,
            "offset_ms": offset_ms,
            "delay_ms":  delay_ms,
            "stratum":   stratum,
            "rand_idx":  rand_idx,
            "next_sec":  next_sec,
            "ok":        ok,
            "error":     error,
            "ts":        now,
            "ts_fmt":    datetime.fromtimestamp(now).strftime("%H:%M:%S"),
        }

        with sqlite3.connect(self._db_path) as conn:
            conn.execute(
                """INSERT INTO ntp_samples
                   (server_host, offset_ms, delay_ms, stratum,
                    rand_idx, next_sec, ok, error, ts)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (server, offset_ms, delay_ms, stratum,
                 rand_idx, next_sec, int(ok), error, now),
            )

        with self._lock:
            self._last_sample = sample
            self._total       += 1

        return sample

    # ── event bus ──────────────────────────────────────────────────────────────

    def subscribe(self, q):
        """Register a queue.Queue to receive every new sample dict."""
        with self._lock:
            self._queues.append(q)

    def _publish(self, event):
        with self._lock:
            qs = self._queues[:]
        for q in qs:
            try:
                q.put_nowait(event)
            except queue.Full:
                pass

    # ── run loop ───────────────────────────────────────────────────────────────

    def _run(self):
        self._refresh_servers()
        countdown = self._rand_int(5, 15)   # first sample within 5–15 s

        while True:
            with self._lock:
                if not self._running:
                    break
                self._next_in = max(0, countdown)

            time.sleep(1)
            countdown -= 1

            if countdown <= 0:
                sample    = self._do_sample()
                self._publish(sample)
                countdown = sample["next_sec"]

    # ── public API ─────────────────────────────────────────────────────────────

    def start(self):
        with self._lock:
            if self._running:
                return
            self._running = True
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def stop(self):
        with self._lock:
            self._running = False

    def status(self):
        db_size = os.path.getsize(self._db_path) if os.path.exists(self._db_path) else 0
        with self._lock:
            return {
                "running":       self._running,
                "total":         self._total,
                "next_in":       self._next_in,
                "servers_count": len(self._servers),
                "db_size_kb":    round(db_size / 1024, 1),
                "last":          self._last_sample,
            }

    def recent(self, n=30):
        with sqlite3.connect(self._db_path) as conn:
            rows = conn.execute(
                """SELECT server_host, offset_ms, delay_ms, stratum,
                          rand_idx, next_sec, ok, error, ts
                   FROM ntp_samples ORDER BY ts DESC LIMIT ?""",
                (n,),
            ).fetchall()
        return [
            {
                "server":    r[0],
                "offset_ms": r[1],
                "delay_ms":  r[2],
                "stratum":   r[3],
                "rand_idx":  r[4],
                "next_sec":  r[5],
                "ok":        bool(r[6]),
                "error":     r[7],
                "ts":        r[8],
                "ts_fmt":    datetime.fromtimestamp(r[8]).strftime("%H:%M:%S"),
            }
            for r in rows
        ]

    def servers(self):
        with self._lock:
            return self._servers[:]

    def db_clear(self):
        with sqlite3.connect(self._db_path) as conn:
            conn.execute("DELETE FROM ntp_samples")
        with self._lock:
            self._total       = 0
            self._last_sample = None
