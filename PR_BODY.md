## Що зроблено

Повний перепис NTP-дашборду з Python/Flask на Go (pure-Go, без CGO).
Збережено всі маршрути, JSON-форми, поведінку SSE і поведінку SQLite,
що їх використовує наявний `dashboard.html`.

### Структура

```
cmd/ntp/             # точка входу (main.go, embed.go з //go:embed dashboard.html)
internal/
├── config/           # конфіг із середовища (NTP_DB, NTP_ADDR, NTP_INTERVAL_MIN/MAX)
├── sampler/          # NTP-рушій + ланцюжок випадковості qrandom.io → random.org → math/rand/v2
├── server/           # Gin HTTP: REST + SSE + SHA-256 auth
├── store/            # SQLite (modernc.org/sqlite): WAL, busy_timeout=3с, міграція rand_src
└── metrics/          # CPU/RAM/disk через gopsutil
Dockerfile            # багатостадійний Go-білд, alpine:3.20, non-root, healthcheck
docker-compose.yml    # той самий volume `ntp_data` і env NTP_DB
Makefile              # build/run/tidy/deploy/docker-deploy (логіка та сама, що в Python-версії)
README.md             # документація REST/SSE API, змінних середовища, auth і валідації
```

### Ключові технічні рішення

* **SQLite** через `modernc.org/sqlite` (без CGO) у WAL; `busy_timeout=3с`;
  блок-схема таблиць 1:1 з Python; авто-міграція колонки `rand_src`.
* **NTP** через `github.com/beevik/ntp` із 5-секундним таймаутом; перший семпл у
  межах 5–15 с, далі — випадковий інтервал 30–120 с (як у Python).
* **Випадковість**: ланцюжок qrandom.io → random.org →
  `math/rand/v2`. timeout=5 с. `rand_src` записується в БД і в SSE-семпл.
* **Heartbeat** кожні 2 с з `DELETE FROM heartbeat ... LIMIT 5000` (як Python).
* **Downtime-детекція** на старті: якщо `now - last_heartbeat > 8с` —
  записуємо `service_restart`. Фінальний heartbeat під час SIGTERM запобігає
  хибному downtime при наступному старті.
* **SSE**: `/events/ntp` стрімить нові семпли й пінг кожні 3 с;
  `/events/metrics` стрімить CPU/RAM/disk кожні ~500 мс. Заголовки
  `Cache-Control: no-cache`, `X-Accel-Buffering: no`.
* **Auth**: SHA-256 пароля
  `acfb20a373bc35f4f6dde55ec29f7f91fd3078ad5192ff1e1b9a02326a4bcc1c` для
  `/auth/verify`, `/ntp/db/clear`, `/ntp/db/export` (відповідає 401 без пароля).
* **Graceful shutdown**: SIGTERM/SIGINT → `sampler.GracefulStop(12s)` →
  `http.Server.Shutdown(15s)`. Фінальний heartbeat запобігає хибному downtime.
* **`dashboard.html` вбудовано в бінарник** через `//go:embed` — окремих
  змін на стороні фронтенду немає.

### Сумісність API з Python-версією

| Метод | Шлях | Відповідь |
|-------|------|-----------|
| GET   | `/ntp/status`       | `running, total, next_in, servers_count, db_size_kb, last` |
| GET   | `/ntp/recent?n=…`   | `{"samples":[…]}` |
| GET   | `/ntp/servers`      | `{"servers":[…]}` |
| GET   | `/ntp/downtime?n=…` | `{"events":[…]}` |
| GET   | `/ntp/uptime-stats` | `uptime_pct, total_down_24h_s, incidents_24h, last_duration_s, last_started_fmt, monitoring_since` |
| GET   | `/ntp/deploys?n=…`  | `{"deploys":[…]}` |
| GET   | `/ntp/server-time`  | `utc, ts, iso, fetched` |
| POST  | `/ntp/deploy`       | `{duration_ms, git_hash, message}` ↦ `{deployed_at, ts_fmt, duration_ms, git_hash, message}` |
| POST  | `/auth/verify`      | `{password}` ↦ `{"ok":bool}` |
| POST  | `/ntp/db/clear`     | `{password}` ↦ `{"status":"cleared"}` / 401 |
| GET   | `/ntp/db/export`    | `?password=…` → gzip (401 без пароля) |
| GET   | `/events/ntp`       | SSE: семпл + ping кожні 3 с |
| GET   | `/events/metrics`   | SSE: CPU/RAM/disk кожні ~500 мс |
| GET   | `/`                 | `dashboard.html` (вбудований) |

### Що видалено

* `server.py`
* `ntp_sampler.py`
* `requirements.txt`

### Інфраструктура

* `Dockerfile` — `golang:1.23-alpine` (CGO=0) → `alpine:3.20`, non-root user,
  healthcheck через curl `/ntp/status`, `EXPOSE 8080`.
* `docker-compose.yml` — той самий том `ntp_data` і env `NTP_DB`,
  healthcheck через curl.
* `Makefile` — `build/run/tidy/clean` для локальної розробки;
  `deploy` (push → pull → `go build` на дроплеті → `systemctl reload` →
  POST `/ntp/deploy` із таймінгом і `git_hash`);
  `docker-deploy` (build → `docker save | gzip | ssh … docker load` без registry).
* `.gitignore` — додано Go-артефакти (`bin/`, `*.exe`, `vendor/`, …) і
  збережено старі Python-винятки (`__pycache__/`, `*.pyc`).

## Чекліст валідації

* [ ] `go mod tidy` проходить без помилок
* [ ] `go build ./cmd/ntp` компілює чистий static бінарник
* [ ] `bin/ntp-dashboard` запускається і слухає `:8080`
* [ ] `curl http://localhost:8080/ntp/status` повертає JSON з очікуваними полями
* [ ] `curl http://localhost:8080/ntp/server-time` повертає `{utc,ts,iso,fetched}`
* [ ] `curl http://localhost:8080/ntp/recent` повертає `{"samples":[…]}`
* [ ] `curl http://localhost:8080/ntp/servers` повертає `{"servers":[…]}`
* [ ] `curl http://localhost:8080/ntp/uptime-stats` повертає числові поля
* [ ] `curl http://localhost:8080/ntp/downtime` повертає `{"events":[…]}`
* [ ] `curl http://localhost:8080/ntp/deploys` повертає `{"deploys":[…]}`
* [ ] SSE-ендпоінти відкриваються через `curl -N` і стрімлять дані з очікуваною
      каденцією (3 с для `/events/ntp`, ~500 мс для `/events/metrics`)
* [ ] Невалідний пароль на `/ntp/db/clear` і `/ntp/db/export` повертає **401**
* [ ] `GET /ntp/db/export?password=…` віддає `ntp_<UTC>.db.gz` як
      `Content-Disposition: attachment`
* [ ] `POST /ntp/db/clear` із валідним паролем повертає `{"status":"cleared"}`,
      і таблиця `ntp_samples` очищена
* [ ] SIGTERM призводить до graceful shutdown без нового `service_restart`
      у `downtime_log` (фінальний heartbeat запобігає хибному downtime)
* [ ] `dashboard.html` рендериться на `/`, фронтенд читає всі поля API без змін

## Поза цим PR

* `easy/` і `.DS_Store` мають локальні модифікації, які не пов'язані з
  переписом NTP-дашборду — не включаються в цей PR.
* Локально на цій машині немає Go-toolchain — `go mod tidy` і `go build`
  мають бути виконані на дроплеті (або скрізь, де є Go 1.23+).

Co-Authored-By: Oz <oz-agent@warp.dev>
