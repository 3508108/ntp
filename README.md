# NTP Sampler

Continuous NTP monitoring service — samples NIST, Cloudflare, Google, and pool.ntp.org servers at quantum-random intervals, persists results to SQLite, and streams data to a live dashboard.

**Live:** http://144.126.244.103:8080

---

## Architecture

```
qrandom.io ──► server selection    ┐
random.org ──► (fallback)          ├─► NTPSampler ──► SQLite (WAL)
local PRNG ──► (fallback)          ┘       │
                                           │ SSE
~31 NTP servers                    Flask ──┴──► Browser Dashboard
  NIST (21)                        gunicorn
  Cloudflare (1)                   systemd
  Google (5)                       DigitalOcean Droplet (fra1)
  pool.ntp.org (4)                 Reserved IP: 144.126.244.103
```

### Components

| File | Role |
|---|---|
| `ntp_sampler.py` | Core: server pool, quantum RNG, NTP queries, SQLite, heartbeat, downtime detection |
| `server.py` | Flask app: REST endpoints, SSE streams, SIGTERM handler |
| `dashboard.html` | Single-page dashboard: live stats, logs, deploy/downtime panels |
| `Makefile` | Developer operations: deploy, logs, status, db, ssh |

### Database (`/var/lib/ntp/ntp.db`)

```sql
ntp_samples   -- offset_ms, delay_ms, stratum, server, rand_src, rand_idx
heartbeat     -- ts every 2s (WAL mode, max 5000 rows kept)
downtime_log  -- started_at, ended_at, duration_s, reason
deploy_log    -- deployed_at, duration_ms, git_hash, message
```

WAL journal mode enables safe concurrent reads during gunicorn rolling reload.

---

## Randomness Chain

Every server selection and sampling interval uses true randomness:

```
1. qrandom.io  — quantum physics (primary)
2. random.org  — atmospheric noise (fallback)
3. random.randint — local PRNG (last resort)
```

Source is recorded per-sample in `ntp_samples.rand_src` and shown in the dashboard log.

---

## Server Pool (~31 servers)

| Source | Servers | Stratum |
|---|---|---|
| NIST (dynamic from tf.nist.gov) | time-a/b/c/d/e/f-g, time-a/b/c/d/e-wwv, time-a/b/c/d/e-b, ntp.nist.gov | 1 |
| Cloudflare | time.cloudflare.com | 3 |
| Google | time.google.com, time1–4.google.com | 1 |
| NTP Pool Project | 0–3.pool.ntp.org | 2 |

**Blocked permanently:** `ntp-b.nist.gov`, `ntp-d.nist.gov`, `ntp-wwv.nist.gov`

---

## Deployment

### Prerequisites

- DigitalOcean droplet `time-sync` (fra1, 512MB, $4/mo)
- Reserved IP `144.126.244.103` assigned to droplet
- Python 3.10+, gunicorn, systemd
- SSH key configured in `~/.ssh/config` as `gr-droplet`

### One-command deploy (zero-downtime)

```bash
make deploy
```

Pushes to GitHub, pulls on the droplet, installs deps, reloads gunicorn gracefully, and records the event in `deploy_log`.

### Other commands

```bash
make logs     # follow journalctl in real-time
make status   # service status + DB file size
make db       # sample count and time range
make ssh      # open SSH session
make restart  # hard restart (use only when necessary)
```

---

## Zero-Downtime Guarantee

### How `make deploy` works

```
git push origin main
    └─► ssh gr-droplet
            git pull
            pip3 install -r requirements.txt
            systemctl reload ntp-dashboard   ← SIGHUP to gunicorn master
            curl POST /ntp/deploy            ← record event in DB
```

`systemctl reload` sends `SIGHUP` to gunicorn which:
1. Forks a **new worker** (new code, new NTPSampler)
2. Old worker receives `SIGTERM` → `_graceful_shutdown()` in `server.py`
3. `graceful_stop()` writes a **final heartbeat** and waits up to 12s for in-flight NTP query
4. Old worker exits cleanly

### Verified results (`scripts/deploy_proof.py`, 100ms probes)

```
RESTART  ·····✗✗✗✗✗···············   93.1%  5 errors  1726ms gap
RELOAD   ··▼·····✗·············       98.8%  1 error   1382ms gap
```

After restart + reload: **0 new entries in downtime_log** — final heartbeat prevents false-positive detection.

### Downtime detection thresholds

```
heartbeat written every 2s
gap > 8s on next startup → recorded in downtime_log

reload gap:  <2s   → NOT recorded  ✓
restart gap: 5–8s  → borderline
crash/stop:  15s+  → recorded      ✓
```

---

## Infrastructure

### Droplet

| Parameter | Value |
|---|---|
| Name | `time-sync` |
| Region | Frankfurt (fra1) |
| Size | 1 vCPU / 512MB RAM / 10GB SSD |
| Cost | $4/month |
| Direct IP | 104.248.21.29 |
| **Reserved IP** | **144.126.244.103** (stable, survives droplet replacement) |

### Systemd service

Key parameters in `/etc/systemd/system/ntp-dashboard.service`:

```ini
--workers 1 --worker-class gthread --threads 4
--graceful-timeout 15          # NTP query timeout 5s + buffer
KillMode=mixed                 # SIGTERM to main, SIGKILL to rest
NTP_DB=/var/lib/ntp/ntp.db    # data dir separate from code
```

### File layout

```
/opt/ntp/          ← code (git-managed, safe to wipe)
  ntp_sampler.py
  server.py
  dashboard.html
  requirements.txt
  Makefile
  scripts/deploy_proof.py

/var/lib/ntp/      ← data (never touched by deploys)
  ntp.db
```

### Firewall (UFW)

```
22/tcp   ALLOW   SSH
8080/tcp ALLOW   Dashboard
```

---

## Secrets & Credentials

Stored in three places — 1Password is primary:

| Secret | 1Password | `.env` key | GitHub Secret |
|---|---|---|---|
| GitHub PAT (classic) | ✅ | `github_classic` | — |
| GitHub PAT (fine-grained) | ✅ | `github` | — |
| DigitalOcean token | ✅ | `digitalocean` | `API_DIGITAL_OCEAN` |
| Droplet root password | ✅ `gr-droplet-root` | `DIGITAL_OCEAN_GR_DROPLET_ROOT` | ✅ |
| SSH private key | ✅ `SSH Key — GR Droplet` | `~/.ssh/id_rsa_gr_droplet` | — |

Global `.env`: `~/Documents/coding/.env` · Shell: `$CODING_ENV`

---

## Dashboard Panels

| Panel | Endpoint | Refresh |
|---|---|---|
| Server time · UTC | `GET /ntp/server-time` | Manual button |
| CPU / RAM / Disk | SSE `/events/metrics` | 500ms |
| NTP stats (offset, delay, stratum) | SSE `/events/ntp` | Per sample |
| Countdown to next sample | SSE ping | 3s |
| NTP sample log | SSE + `/ntp/recent` | Real-time |
| Uptime stats | `GET /ntp/uptime-stats` | 30s |
| Downtime log | `GET /ntp/downtime` | 30s |
| Deploy log | `GET /ntp/deploys` | 30s |

---

## API Reference

```
GET  /ntp/status          sampler status (running, total, next_in)
GET  /ntp/recent?n=50     last N samples
GET  /ntp/servers         current server pool list
GET  /ntp/downtime?n=20   recent downtime events
GET  /ntp/uptime-stats    uptime %, incidents, last event
GET  /ntp/deploys?n=20    recent deploy events
GET  /ntp/server-time     current server UTC time + timestamp
POST /ntp/deploy          record a deploy event {duration_ms, git_hash, message}
POST /ntp/db/clear        clear ntp_samples (heartbeat/downtime preserved)

GET  /events/ntp          SSE: new samples + status pings
GET  /events/metrics      SSE: CPU/RAM/disk every 500ms
```
