# NTP Dashboard (Go)

Платформа для безперервного NTP-семплінгу з істинною випадковістю
(qrandom.io → random.org → локальний PRNG), що зберігає результати в SQLite
і віддає REST/SSE-метрики для `dashboard.html`.

Ця версія є повним переписом оригінального Python/Flask-проєкту на Go
(див. історію комітів).

## Структура

```
.
├── cmd/ntp/             # точка входу (main.go, embed.go)
├── internal/
│   ├── config/          # конфіг із середовища
│   ├── sampler/         # NTP-рушій + ланцюжок випадковості
│   ├── server/          # HTTP-обробники + SSE + auth
│   ├── store/           # SQLite (modernc/sqlite, WAL)
│   └── metrics/         # CPU/RAM/disk через gopsutil
├── dashboard.html       # без змін, вбудовується в бінарник
├── Dockerfile           # багатостадійний Go-білд
├── docker-compose.yml
├── Makefile
├── go.mod / go.sum
└── .gitignore
```

## Збірка та запуск

Потрібен Go 1.23+. На дроплеті використовується шлях `/usr/local/go/bin/go`.

```bash
# локальна збірка
go mod tidy
go build -o bin/ntp-dashboard ./cmd/ntp
./bin/ntp-dashboard
```

Або через Makefile:

```bash
make build
make run
```

Або Docker:

```bash
make docker-build
make docker-run
```

## Змінні середовища

| Змінна          | За замовчування     | Опис                                    |
|-----------------|---------------------|-----------------------------------------|
| `NTP_DB`        | `ntp.db`            | Шлях до SQLite                          |
| `NTP_ADDR`      | `:8080`             | HTTP-адреса                             |
| `NTP_INTERVAL_MIN` | `30`             | Мінімальний інтервал NTP-семплінгу (с)  |
| `NTP_INTERVAL_MAX` | `120`            | Максимальний інтервал NTP-семплінгу (с) |

## Деплой

Логіка та сама, що в оригінальному Makefile (push → pull → rebuild →
systemctl reload → POST /ntp/deploy), але замість `pip install + gunicorn`
тепер `go build + systemd`:

```bash
make deploy          # → /opt/ntp на gr-droplet
make docker-deploy   # → docker image streamed via ssh
```

## REST API (ідентичний Python-реалізації)

* `GET /ntp/status` — `running`, `total`, `next_in`, `servers_count`,
  `db_size_kb`, `last`.
* `GET /ntp/recent?n=50` — список `samples` (макс. 200).
* `GET /ntp/servers` — `servers`.
* `GET /ntp/downtime?n=20` — `events`.
* `GET /ntp/uptime-stats`.
* `GET /ntp/deploys?n=20` — `deploys`.
* `GET /ntp/server-time` — `utc`, `ts`, `iso`, `fetched`.
* `POST /ntp/deploy` — `{duration_ms, git_hash, message}` → returns deploy event.
* `POST /auth/verify` — `{password}` → `{ok: bool}`.
* `POST /ntp/db/clear` — `{password}` → `{status: "cleared"}` (401 без пароля).
* `GET /ntp/db/export?password=...` — gzip-дамп БД (401 без пароля).
* `GET /events/ntp` — SSE: новий NTP-семпл, ping кожні 3с.
* `GET /events/metrics` — SSE: CPU/RAM/disk кожні ~1с.

## Auth

SHA-256 пароля на критичних ендпоінтах збігається з Python:

```
acfb20a373bc35f4f6dde55ec29f7f91fd3078ad5192ff1e1b9a02326a4bcc1c
```

## Перевірено

* `go build ./cmd/ntp` — компіляція проходить (pure-Go, без CGO).
* Усі шляхи, JSON-форми й поведінка SSE збігаються з Python-версією.
* WAL-режим, busy-timeout 3с і міграція колонки `rand_src` відтворені.
* Graceful shutdown: SIGTERM → sampler.GracefulStop → server.Shutdown
  з таймаутом 15с; фінальний heartbeat запобігає хибному downtime.

## Нотатки для валідації на дроплеті

* Перевірити, що на дроплеті є Go 1.23+ (або додати в Makefile).
* Після деплою: `systemctl status ntp-dashboard`,
  `curl http://localhost:8080/ntp/status`.
* `make docker-deploy` потребує запуску `docker build` на локальній машині
  (cross-compile linux/amd64) і стрімінгу образу через ssh — без registry.
