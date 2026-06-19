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
