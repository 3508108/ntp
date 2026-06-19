# PROJECT — ntp/easy

## 1) Поточний scope
`easy/` — єдиний активний кодовий проєкт у репозиторії для сервісу NTP-проб.

Legacy root-кодова база (`ntp-dashboard` у корені репозиторію) видалена, щоб залишити одну актуальну реалізацію.

## 2) Сервіс easy (актуально)
- Мова: Go
- База: SQLite (WAL)
- Запуск у проді: systemd unit `easy`
- Бінарник: `/opt/easy/easy`
- Робоча директорія: `/opt/easy`
- API порт: `8080` (через nginx назовні)

### Змінні середовища
- `EASY_DB=/var/lib/easy/easy.db`
- `EASY_PORT=8080`
- `EASY_INTERVAL=10s`

### Endpoint-и
- `GET /`
- `GET /api/recent`
- `GET /api/stream`
- `GET /api/interval`
- `POST /api/interval`
- `POST /0000`

## 3) Поточна DNS-інфраструктура (DigitalOcean)
Активні тільки 2 сервери:

- `fra-dns`
  - ID: `578352080`
  - IPv4: `64.226.73.107`
  - IPv6: `2a03:b0c0:3:f0:0:2:8fa9:e000`
- `fra-dns-2`
  - ID: `578356929`
  - IPv4: `165.227.157.91`
  - IPv6: `2a03:b0c0:3:f0:0:2:8fbc:f000`

Видалений як зайвий:
- `fra-dns-3` (ID `578910227`)

## 4) DNS-конфіг на обох серверах
- BIND9 recursive resolver
- Root hints ротація всіх 13 root-серверів (A–M) кожні 2 години
- Таймери:
  - `root-hints-rotate.timer` (2h)
  - `dns-health-check.timer` (5m)
- Логи:
  - `/var/log/named/named.log`
  - `/var/log/named/queries.log`
  - `/var/log/named/health.log`
- UFW:
  - allow `22/tcp`, `53/tcp`, `53/udp` (IPv4 + IPv6)
  - default deny incoming

## 5) Доступ і політика
- Серверне підключення: спочатку тільки `root`.
- SSH: тільки RSA-ключі.
- 1Password vault `auto` — джерело істини для доступів/секретів/інфраструктурних нотаток.

## 6) 1Password (актуальні записи)
- `INFRA — DNS (DigitalOcean) — Поточний стан` — ID `uxflxk3ryi3z2lg6b4ypjpzzya`
- `POLICY — Інфраструктура доступів (strict 1Password)` — ID `n4bqgm5xermcdmz5bdab5d3rxm`
- `GitHub Classic PAT — EXPOSED — ROTATION REQUIRED`
- `GitHub Fine-grained PAT — PENDING FILL`

## 7) Безпека
- PAT-рядки (`ghp_`, `github_pat_`) видалені з коду/локальної history.
- Якщо токен був вставлений у чат/термінал, він вважається скомпрометованим і має бути відкликаний у GitHub UI.

## 8) Фінальна верифікація та закриття
- Дата фінальної перевірки: `2026-06-19`.
- DNS-сервери в продакшені: тільки `fra-dns` і `fra-dns-2`.
- `fra-dns-3` видалено як зайвий.
- Результат health-check на обох серверах: `ok` (`dns-health-check.js` exit code `0`).
- `bind9`, `root-hints-rotate.timer`, `dns-health-check.timer`: `active` на обох серверах.
- Інфраструктура зафіксована як фінальна для поточного етапу.
