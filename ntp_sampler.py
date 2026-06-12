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

NIST_URL      = "https://tf.nist.gov/tf-cgi/servers.cgi"
QRANDOM_URL   = "https://qrandom.io/api/random/ints?min={min}&max={max}&n={n}"
RANDORG_URL   = (
    "https://www.random.org/integers/"
    "?num={n}&min={min}&max={max}&col=1&base=10&format=plain&rnd=new"
)

HEARTBEAT_INTERVAL = 2     # seconds between heartbeats
DOWNTIME_THRESHOLD = 8     # gap > this → recorded as downtime (restart ~5-8s, reload <2s)

FALLBACK_SERVERS = [
    "time-a-g.nist.gov",   "time-b-g.nist.gov",   "time-c-g.nist.gov",
    "time-d-g.nist.gov",   "time-e-g.nist.gov",   "time-f-g.nist.gov",
    "time-a-wwv.nist.gov", "time-b-wwv.nist.gov",  "time-c-wwv.nist.gov",
    "time-d-wwv.nist.gov", "time-e-wwv.nist.gov",
    "time-a-b.nist.gov",   "time-b-b.nist.gov",    "time-c-b.nist.gov",
    "time-d-b.nist.gov",   "time-e-b.nist.gov",
    "ntp.nist.gov",
]
_SKIP = ("www.", "ftp.", "mail.", "smtp.")

# Servers permanently excluded from the pool
BLOCKED_SERVERS = {
    "ntp-b.nist.gov",
    "ntp-d.nist.gov",
    "ntp-wwv.nist.gov",
}

# Extra servers always merged into the pool (non-NIST sources)
EXTRA_SERVERS = [
    "time.cloudflare.com",   # Cloudflare — anycast, ~1ms global
    "time.google.com",       # Google Public NTP
    "time1.google.com",
    "time2.google.com",
    "time3.google.com",
    "time4.google.com",
    "0.pool.ntp.org",        # NTP Pool Project
    "1.pool.ntp.org",
    "2.pool.ntp.org",
    "3.pool.ntp.org",
]


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
        self._hb_thread     = None
        self._init_db()
        self._detect_startup_downtime()
        self._refresh_servers()

    # ── database ───────────────────────────────────────────────────────────────

    def _init_db(self):
        with sqlite3.connect(self._db_path) as conn:
            conn.execute("PRAGMA journal_mode=WAL")  # safe concurrent access during reload
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
            conn.execute("""
                CREATE TABLE IF NOT EXISTS heartbeat (
                    id  INTEGER PRIMARY KEY AUTOINCREMENT,
                    ts  REAL NOT NULL
                )
            """)
            conn.execute("""
                CREATE TABLE IF NOT EXISTS downtime_log (
                    id          INTEGER PRIMARY KEY AUTOINCREMENT,
                    started_at  REAL NOT NULL,
                    ended_at    REAL NOT NULL,
                    duration_s  REAL NOT NULL,
                    reason      TEXT DEFAULT 'service_restart'
                )
            """)
            conn.execute(
                "CREATE INDEX IF NOT EXISTS idx_hb_ts ON heartbeat(ts)"
            )
            conn.execute("""
                CREATE TABLE IF NOT EXISTS deploy_log (
                    id          INTEGER PRIMARY KEY AUTOINCREMENT,
                    deployed_at REAL    NOT NULL,
                    duration_ms INTEGER,
                    git_hash    TEXT,
                    message     TEXT
                )
            """)
            row = conn.execute("SELECT COUNT(*) FROM ntp_samples").fetchone()
            self._total = row[0] if row else 0

    def _detect_startup_downtime(self):
        """On every startup: check last heartbeat. Gap > threshold = downtime."""
        now = time.time()
        with sqlite3.connect(self._db_path) as conn:
            row = conn.execute(
                "SELECT ts FROM heartbeat ORDER BY ts DESC LIMIT 1"
            ).fetchone()
            if row:
                last_hb = row[0]
                gap = now - last_hb
                if gap > DOWNTIME_THRESHOLD:
                    started_at = last_hb + HEARTBEAT_INTERVAL
                    duration_s = round(now - started_at, 1)
                    conn.execute(
                        """INSERT INTO downtime_log
                           (started_at, ended_at, duration_s, reason)
                           VALUES (?, ?, ?, ?)""",
                        (started_at, now, duration_s, "service_restart"),
                    )

    # ── server list ────────────────────────────────────────────────────────────

    def _refresh_servers(self):
        """Download the NIST server list; use embedded fallback on failure.
        Always appends EXTRA_SERVERS (Cloudflare, Google, pool.ntp.org).
        """
        nist = []
        if _HAS_REQUESTS:
            try:
                resp       = _requests.get(NIST_URL, timeout=10)
                candidates = re.findall(
                    r"\b([a-z0-9][a-z0-9\-\.]*\.nist\.gov)\b", resp.text
                )
                nist = [
                    s for s in dict.fromkeys(candidates)
                    if not any(s.startswith(p) for p in _SKIP)
                ]
            except Exception:
                pass
        if len(nist) < 5:
            nist = FALLBACK_SERVERS[:]
        # Merge: NIST first, then extra — filter blocked — deduplicate
        merged = [
            s for s in dict.fromkeys(nist + EXTRA_SERVERS)
            if s not in BLOCKED_SERVERS
        ]
        with self._lock:
            self._servers = merged

    # ── true randomness ────────────────────────────────────────────────────────

    def _rand_int(self, lo, hi):
        """Quantum int from qrandom.io → random.org → random.randint fallback.
        Returns (value, source) tuple.
        """
        if not _HAS_REQUESTS or lo == hi:
            return random.randint(lo, hi), "local"

        # 1) qrandom.io — quantum RNG
        try:
            url  = QRANDOM_URL.format(min=lo, max=hi, n=1)
            resp = _requests.get(url, timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                nums = data.get("numbers") or data.get("number")
                if nums and isinstance(nums, list) and len(nums) > 0:
                    return int(nums[0]), "qrandom"
        except Exception:
            pass

        # 2) random.org — atmospheric noise RNG
        try:
            url  = RANDORG_URL.format(n=1, min=lo, max=hi)
            resp = _requests.get(url, timeout=5)
            val  = resp.text.strip()
            if resp.status_code == 200 and val.lstrip("-").isdigit():
                return int(val), "random.org"
        except Exception:
            pass

        # 3) local PRNG fallback
        return random.randint(lo, hi), "local"

    # ── single NTP sample ──────────────────────────────────────────────────────

    def _do_sample(self):
        with self._lock:
            servers = self._servers[:]

        rand_idx, rand_src   = self._rand_int(0, len(servers) - 1)
        next_sec, _          = self._rand_int(self._interval_min, self._interval_max)
        server               = servers[rand_idx]
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
            "rand_src":  rand_src,
            "next_sec":  next_sec,
            "ok":        ok,
            "error":     error,
            "ts":        now,
            "ts_fmt":    datetime.fromtimestamp(now).strftime("%H:%M:%S"),
        }

        with sqlite3.connect(self._db_path) as conn:
            # add rand_src column if it doesn't exist (migration)
            cols = [r[1] for r in conn.execute("PRAGMA table_info(ntp_samples)").fetchall()]
            if "rand_src" not in cols:
                conn.execute("ALTER TABLE ntp_samples ADD COLUMN rand_src TEXT")
            conn.execute(
                """INSERT INTO ntp_samples
                   (server_host, offset_ms, delay_ms, stratum,
                    rand_idx, next_sec, ok, error, rand_src, ts)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (server, offset_ms, delay_ms, stratum,
                 rand_idx, next_sec, int(ok), error, rand_src, now),
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

    # ── heartbeat ──────────────────────────────────────────────────────────────

    def _heartbeat_loop(self):
        while True:
            with self._lock:
                if not self._running:
                    break
            now = time.time()
            with sqlite3.connect(self._db_path) as conn:
                conn.execute("INSERT INTO heartbeat (ts) VALUES (?)", (now,))
                # keep only last 5000 heartbeats (~7 hours)
                conn.execute(
                    "DELETE FROM heartbeat WHERE id NOT IN "
                    "(SELECT id FROM heartbeat ORDER BY ts DESC LIMIT 5000)"
                )
            time.sleep(HEARTBEAT_INTERVAL)

    # ── run loop ───────────────────────────────────────────────────────────────

    def _run(self):
        self._refresh_servers()
        countdown, _ = self._rand_int(5, 15)   # first sample within 5–15 s

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
        self._thread    = threading.Thread(target=self._run,           daemon=True)
        self._hb_thread = threading.Thread(target=self._heartbeat_loop, daemon=True)
        self._thread.start()
        self._hb_thread.start()

    def stop(self):
        with self._lock:
            self._running = False

    def graceful_stop(self, timeout=12):
        """Called on SIGTERM: finish in-flight sample, write final heartbeat, then stop.
        Safe to call from signal handler.
        """
        with self._lock:
            self._running = False
        # write a final heartbeat so no false-positive downtime is recorded
        try:
            with sqlite3.connect(self._db_path, timeout=3) as conn:
                conn.execute("INSERT INTO heartbeat (ts) VALUES (?)", (time.time(),))
        except Exception:
            pass
        # wait for the NTP sample thread to finish its current query
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=timeout)
        if self._hb_thread and self._hb_thread.is_alive():
            self._hb_thread.join(timeout=2)

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
                          rand_idx, next_sec, ok, error, rand_src, ts
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
                "rand_src":  r[8] or "local",
                "ts":        r[9],
                "ts_fmt":    datetime.fromtimestamp(r[9]).strftime("%H:%M:%S"),
            }
            for r in rows
        ]

    def servers(self):
        with self._lock:
            return self._servers[:]

    def downtime_recent(self, n=20):
        """Return n most recent downtime events."""
        with sqlite3.connect(self._db_path) as conn:
            rows = conn.execute(
                """SELECT started_at, ended_at, duration_s, reason
                   FROM downtime_log ORDER BY started_at DESC LIMIT ?""",
                (n,),
            ).fetchall()
        return [
            {
                "started_at":  r[0],
                "ended_at":    r[1],
                "duration_s":  r[2],
                "reason":      r[3],
                "started_fmt": datetime.fromtimestamp(r[0]).strftime("%H:%M:%S"),
                "date_fmt":    datetime.fromtimestamp(r[0]).strftime("%d %b"),
            }
            for r in rows
        ]

    def uptime_stats(self):
        """Return uptime percentage and summary since first heartbeat."""
        now        = time.time()
        day_start  = now - 86400   # last 24 h
        with sqlite3.connect(self._db_path) as conn:
            # first heartbeat ever
            first = conn.execute("SELECT MIN(ts) FROM heartbeat").fetchone()[0]
            # total downtime in last 24h
            rows = conn.execute(
                """SELECT COALESCE(SUM(duration_s), 0), COUNT(*)
                   FROM downtime_log WHERE started_at >= ?""",
                (day_start,),
            ).fetchone()
            total_down_24h, incidents_24h = rows
            # last incident
            last = conn.execute(
                "SELECT duration_s, started_at FROM downtime_log ORDER BY started_at DESC LIMIT 1"
            ).fetchone()
            # all-time downtime
            all_down_row = conn.execute(
                "SELECT COALESCE(SUM(duration_s),0) FROM downtime_log"
            ).fetchone()

        window     = now - (first or now)
        all_down   = all_down_row[0] if first else 0
        uptime_pct = round(100 * (1 - all_down / window), 3) if window > 0 else 100.0

        return {
            "uptime_pct":       uptime_pct,
            "total_down_24h_s": round(total_down_24h, 1),
            "incidents_24h":    incidents_24h,
            "last_duration_s":  round(last[0], 1) if last else None,
            "last_started_fmt": datetime.fromtimestamp(last[1]).strftime("%d %b %H:%M") if last else None,
            "monitoring_since": datetime.fromtimestamp(first).strftime("%d %b %H:%M") if first else None,
        }

    def log_deploy(self, duration_ms=None, git_hash=None, message=None):
        """Record a deploy event."""
        now = time.time()
        with sqlite3.connect(self._db_path) as conn:
            conn.execute(
                """INSERT INTO deploy_log (deployed_at, duration_ms, git_hash, message)
                   VALUES (?, ?, ?, ?)""",
                (now, duration_ms, git_hash, (message or "")[:120]),
            )
        return {
            "deployed_at": now,
            "ts_fmt":      datetime.fromtimestamp(now).strftime("%d %b %H:%M:%S"),
            "duration_ms": duration_ms,
            "git_hash":    git_hash,
            "message":     message,
        }

    def deploys_recent(self, n=20):
        """Return n most recent deploys."""
        with sqlite3.connect(self._db_path) as conn:
            rows = conn.execute(
                """SELECT deployed_at, duration_ms, git_hash, message
                   FROM deploy_log ORDER BY deployed_at DESC LIMIT ?""",
                (n,),
            ).fetchall()
        return [
            {
                "deployed_at": r[0],
                "duration_ms": r[1],
                "git_hash":    r[2] or "",
                "message":     r[3] or "",
                "ts_fmt":      datetime.fromtimestamp(r[0]).strftime("%H:%M:%S"),
                "date_fmt":    datetime.fromtimestamp(r[0]).strftime("%d %b"),
            }
            for r in rows
        ]

    def db_clear(self):
        with sqlite3.connect(self._db_path) as conn:
            conn.execute("DELETE FROM ntp_samples")
        with self._lock:
            self._total       = 0
            self._last_sample = None
