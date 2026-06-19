# ntp/easy
Легкий Go-сервіс, який періодично опитує NTP-сервери (`time.apple.com`, `time.google.com`, `time.nist.gov`) і віддає дані через HTTP API та SSE.

## Поточна архітектура
- `internal/fetcher` — UDP NTP-опитування трьох джерел у паралелі.
- `internal/store` — SQLite (WAL), таблиці `time_log` і `ping_0000`.
- `internal/server` — Gin API + SSE + вбудований HTML дашборд.
- `main.go` — запуск fetch loop, HTTP-сервера, graceful shutdown.

## API
- `GET /` — HTML dashboard.
- `GET /api/recent` — останні записи (`rows`).
- `GET /api/stream` — SSE-потік оновлень.
- `GET /api/interval` — поточний інтервал опитування.
- `POST /api/interval` — встановлення нового інтервалу (`{"interval":"10s"}`).
- `POST /0000` — запис мобільного ping (`time`, `timestamp`, `device`, `action`).

## Змінні середовища
- `EASY_DB` (default: `easy.db`)
- `EASY_PORT` (default: `8080`)
- `EASY_INTERVAL` (default: `10s`, мінімум у коді: `5s`)

## Локальний запуск
```bash
go build ./...
go run .
```

## Деплой (systemd)
Сервіс у проді запускається як systemd unit `easy` (див. `easy.service`).

Через Makefile:
```bash
make deploy
```

Або вручну:
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o easy-linux .
scp easy-linux easy-droplet:/opt/easy/easy
ssh easy-droplet 'chmod +x /opt/easy/easy && systemctl restart easy && systemctl status easy --no-pager'
```

## Примітка по доступу
- Для серверного доступу використовується `root` + RSA-ключ.

## Моніторинг і статистика DNS
Моніторинг налаштований синхронно на двох DNS-серверах:
- `fra-dns` (`64.226.73.107`, `2a03:b0c0:3:f0:0:2:8fa9:e000`)
- `fra-dns-2` (`165.227.157.91`, `2a03:b0c0:3:f0:0:2:8fbc:f000`)

### Скрипти
- `/usr/local/sbin/dns-health-check.js` — перевірка DNS по IPv4/IPv6, логування `status`, `answer_count`, `latency_ms`.
- `/usr/local/sbin/dns-alert-evaluator.js` — оцінка деградації (consecutive fails), запис подій у alerts лог, опційний Telegram.
- `/usr/local/sbin/dns-stats-aggregate.js` — агрегація статистики за 24h/7d.

### Systemd таймери
- `dns-health-check.timer` — кожні 5 хвилин.
- `dns-alert-evaluator.timer` — кожні 5 хвилин.
- `dns-stats-aggregate.timer` — кожні 15 хвилин.
- `root-hints-rotate.timer` — кожні 2 години.

### Артефакти моніторингу
- Логи:
  - `/var/log/named/health.log`
  - `/var/log/named/alerts.log`
  - `/var/log/named/stats.log`
- JSON статистика:
  - `/var/log/named/stats/latest.json`
  - `/var/log/named/stats/24h.json`
  - `/var/log/named/stats/7d.json`

### Результати останньої верифікації
- На обох серверах health-check має `status=ok`.
- `alerts.log`: `action=none`, `consecutive_fails=0`.
- `24h.json` і `7d.json`: `failed=0`, `uptime_percent=100`.
- `latest.json`: `latest_status=ok`.
