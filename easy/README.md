# ntp/easy — time probes

Lightweight Go service that continuously polls remote NTP endpoints and displays time deltas in real-time.

## Dashboard

https://time.karpenkodima0000.com

## Architecture

- `internal/fetcher` — polls 4 endpoints via HTTPS + Basic Auth, stores results
- `internal/store` — SQLite (WAL mode) with `time_log` and `ping_0000` tables
- `internal/server` — Gin HTTP server with SSE streaming and embedded HTML dashboard

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | HTML dashboard |
| GET | `/api/recent` | Last 500 time_log rows |
| GET | `/api/stream` | SSE real-time updates |
| POST | `/0000` | Log device ping (iPhone, Android, etc.) |

## Deploy

```bash
make deploy
```

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `EASY_DB` | `easy.db` | SQLite path |
| `EASY_PORT` | `8000` | HTTP port |
| `EASY_INTERVAL_MS` | `500ms` | Poll interval |

## Tables

**time_log** — polls from remote NTP servers
- `probe`, `date_time`, `unix_ms`, `server_ms`, `cloudflare_ms`, `ntp_name`

**ping_0000** — mobile device pings
- `time_str`, `timestamp`, `device`, `action`

## Droplet

- Name: `easy`
- IP: `164.92.206.61` (fra1)
- Size: 1 vCPU / 512MB / 10GB SSD
- Domain: `time.karpenkodima0000.com` (A → `165.22.26.212`)

## SSH

```bash
ssh easy-droplet
```

Uses RSA key `~/.ssh/golden-ratio` only.
