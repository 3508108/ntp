# ntp/easy — Go Time Probe Service

## Overview

Lightweight Go service that polls NTP servers (apple.com, google.com, nist.gov) every N seconds and serves a real-time dashboard. Also accepts mobile device time pings via `POST /0000`.

## Architecture

- **Direct NTP UDP queries** — no HTTPS proxies, queries `time.apple.com`, `time.google.com`, `time.nist.gov` in parallel (3 concurrent goroutines per cycle)
- **Single loop** — fetch → store → wait for interval
- **Interval** — configurable via dashboard UI and `GET/POST /api/interval` (default: 10s)
- **Storage** — SQLite with WAL mode, `sync.Mutex` for write serialization
- **Streaming** — SSE endpoint `/events` pushes new rows in real-time
- **Dashboard** — embedded HTML/JS served at `/` with table, interval controls, and live updates

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Dashboard HTML |
| GET | `/api/recent` | Last 50 NTP + ping rows (JSON) |
| GET | `/api/interval` | Current polling interval |
| POST | `/api/interval` | Set interval (`{"interval":"10s"}`) |
| POST | `/0000` | Log mobile device ping (`{"time":"...","timestamp":"...","device":"iPhone","action":"time_ping"}`) |
| GET | `/events` | SSE stream of new rows |

## Deployment

- **Droplet**: `easy` in DO project `ntp_tima`, IP `165.22.26.212`, fra1, 512MB
- **SSH**: `~/.ssh/ntp-tima/rsa` (4096-bit RSA), host alias `easy-droplet` in `~/.ssh/config`
- **Binary**: `/opt/easy/easy`, static Linux amd64, ~11-12MB
- **Systemd**: `easy.service` runs as root on `:8080`
- **Nginx**: reverse proxy on port 80/443, config at `/etc/nginx/sites-available/karpenkodima0000.com`

## DNS Records (karpenkodima0000.com)

| Type | Name | Data | TTL |
|------|------|------|-----|
| A | @ | 165.22.26.212 | 300 |
| A | ntp | 165.22.26.212 | 300 |
| A | time | 165.22.26.212 | 300 |

## SSL Certificates

- **karpenkodima0000.com** — Let's Encrypt via certbot (nginx plugin), auto-renewal enabled
- Certificate path: `/etc/letsencrypt/live/karpenkodima0000.com/`

## 1Password Secrets

- **DigitalOcean API Token + root** — item `zmhn5td2dv6x4qf2ztmxhzj6vu`, field `credential` — used for DNS management via DO API

## Quick Commands

```bash
# SSH
ssh easy-droplet

# Deploy binary
make deploy          # builds + scp + systemctl restart

# Logs
ssh easy-droplet 'journalctl -u easy -f'

# Check services
ssh easy-droplet 'systemctl status easy nginx'

# Test API
curl https://karpenkodima0000.com/api/interval
curl https://karpenkodima0000.com/api/recent

# DNS via 1Password token (example)
TOKEN=$(op item get zmhn5td2dv6x4qf2ztmxhzj6vu --reveal --fields credential)
curl -H "Authorization: Bearer $TOKEN" https://api.digitalocean.com/v2/domains/karpenkodima0000.com/records
```

## Notes

- Port 8080 is intentionally **not firewalled** — nginx proxies all external traffic
- Mobile pings go to `ping_0000` table; NTP results go to `ntp_results`
- All timestamps stored as UTC, displayed in local browser time via JS

---

# Legacy / Deprecated (old droplet + homepage with password)

## 1. Загальна інформація (old)

- **Назва**: `ntp/easy` — система опитування NTP-серверів + landing page з паролем
- **Репозиторій**: локальний git у `/Users/dimakarpenko/Documents/coding/digital ocean/ntp/easy/`
- **Гілка**: `main`
- **Деплой**: DigitalOcean droplet (IP: `64.226.102.81`, тег `ntp-droplet` в 1Password)
- **Віддалений доступ**: `ssh easy-droplet` (Makefile) або `ssh root@64.226.102.81`
- **SSH ключ**: `~/.ssh/golden-ratio` (RSA, теги `ssh`, `digitalocean`, `golden-ratio` в 1Password)
- **GitHub PAT**: `{{GITHUB_PAT}}` (зберігається в 1Password як `GitHub Classic PAT`, теги `github`, `api-key`, `golden-ratio`)
- **Статус деплою**: GitHub push не працює — `remote: Repository not found` (можливо, репозиторій приватний/перейменований або токен не має прав)

---

## 2. Current Architecture (ntp_tima droplet)

### 2.1. Сервіс `easy` (Go)

- **Бінарник**: `/opt/easy/easy` (static Linux amd64)
- **База даних**: SQLite (`easy.db`) в WAL-режимі
- **Таблиці**:
  - `ntp_results` — опитування NTP (probe, server, server_ms, client_ms, offset_ms, rtt_ms)
  - `ping_0000` — пінги мобільних пристроїв
- **Пакети**:
  - `internal/fetcher` — прямі NTP UDP запити (beevik/ntp)
  - `internal/store` — робота з SQLite
  - `internal/server` — Gin HTTP сервер з SSE стрімінгом
- **Endpoints**:
  - `GET /` — HTML dashboard (вбудований у бінарник)
  - `GET /api/recent` — останні 50 рядків
  - `GET /api/interval` — поточний інтервал
  - `POST /api/interval` — зміна інтервалу
  - `POST /0000` — логування пінгу пристрою
  - `GET /events` — SSE real-time updates
- **Налаштування** (env):
  - `EASY_DB` — шлях до SQLite (default: `easy.db`)
  - `EASY_PORT` — HTTP порт (default: `8080`)
  - `EASY_INTERVAL` — інтервал опитування (default: `10s`)
- **Домен**: `karpenkodima0000.com`, `ntp.karpenkodima0000.com`, `time.karpenkodima0000.com` → `165.22.26.212`

### 2.2. Nginx + SSL

- **Конфіг**: `/etc/nginx/sites-available/karpenkodima0000.com`
- **SSL**: Let's Encrypt (certbot --nginx)
- **Проксі**: `location /api/` → `localhost:8080`
- **Авто-оновлення**: certbot timer активний

---

## 3. Homepage (Legacy — old droplet)

(Deprecated — old password-protected landing page on 64.226.102.81)

---

## 4. Docker Compose (Legacy)

(Deprecated — current deployment uses systemd + nginx on the droplet, not Docker)

---

## 5. Current Deployment (ntp_tima droplet)

### 5.1. Deploy flow

```bash
# Build
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o easy-linux .

# Copy
scp -i ~/.ssh/ntp-tima/rsa easy-linux root@165.22.26.212:/opt/easy/easy

# Restart
ssh -i ~/.ssh/ntp-tima/rsa root@165.22.26.212 'systemctl restart easy'
```

### 5.2. SSH config

```
Host easy-droplet
  HostName 165.22.26.212
  User root
  IdentityFile ~/.ssh/ntp-tima/rsa
  IdentitiesOnly yes
```

### 5.3. Systemd unit

`/etc/systemd/system/easy.service` — runs `/opt/easy/easy`, WorkingDirectory `/opt/easy`, Restart=always

---

## 6. 1Password Credentials

| Item | ID (в 1Password) | Поля | Призначення |
|------|------------------|------|-------------|
| DigitalOcean API Token + root | `zmhn5td2dv6x4qf2ztmxhzj6vu` | `credential` | DO API для DNS (створення A-записів ntp/time) |
| ntp-tima SSH key | `~/.ssh/ntp-tima/rsa` | private key | SSH до `165.22.26.212` |

---

## 7. Домени та DNS (current)

| Домен | IP | Сервіс | Порт | Примітки |
|-------|----|--------|------|----------|
| `karpenkodima0000.com` | 165.22.26.212 | nginx + easy | 80/443 | Apex domain, homepage + API proxy |
| `ntp.karpenkodima0000.com` | 165.22.26.212 | easy | 8080 | NTP dashboard (duplicate) |
| `time.karpenkodima0000.com` | 165.22.26.212 | easy | 8080 | NTP dashboard (duplicate) |

---

## 8. Git історія (current ntp/easy)

(Див. `git log --oneline -10` у репозиторії)

---

## 9. Структура файлів (current)

```
ntp/easy/
├── PROJECT.md
├── README.md
├── Makefile
├── easy.service
├── Dockerfile
├── docker-compose.yml
├── main.go
├── go.mod / go.sum
├── easy (darwin binary)
├── easy-linux (linux binary)
├── internal/
│   ├── fetcher/fetcher.go
│   ├── server/server.go
│   └── store/db.go
└── (no homepage/ — nginx serves static root on droplet)
```

---

## 10. Current Status

- DNS: apex + ntp + time all point to 165.22.26.212 via DO API (token from 1Password)
- SSL: Let's Encrypt active on karpenkodima0000.com
- Service: easy running via systemd, nginx proxying API and serving homepage
- Homepage: static root served by nginx (no password gate)

---
