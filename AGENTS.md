# Project Rules — ntp

## Dashboard link rule

> **ALWAYS** include a link to the live dashboard after any deploy, code change, or update.

After every `make deploy`, file change, or any action that modifies the running service,
include this line in the response:

**Dashboard:** http://144.126.244.103:8080

## Credential management

> Global rule applies: **always save to 1Password first** (vault: Personal).
> Central `.env` is at `~/Documents/coding/.env` — source it in any project shell.

Credentials used by this project, all stored in 1Password (Personal vault):

| Variable | 1Password item | Notes |
|---|---|---|
| `DROPLET_HOST` | `144.126.244.103` (Reserved IP) | Stable public endpoint |
| `DIGITAL_OCEAN_GR_DROPLET_ROOT` | `gr-droplet-root` (Login) | Root password |
| `github` / `github_classic` | `GitHub Personal Access Token`, `GitHub Classic PAT` | GitHub PATs for CI/CD |
| `digitalocean` | DigitalOcean API token | Droplet management |
| SSH private key | `SSH Key — GR Droplet` | `~/.ssh/id_rsa_gr_droplet` |

Rotation checklist:
1. Update value in 1Password
2. Update `~/Documents/coding/.env`
3. Update GitHub Secret via repo Settings → Secrets

Never commit `.env` or `.pat` or any raw credential to git.
`.gitignore` covers: `.env`, `.pat`, `*.db`, `*.pyc`.

## Security

- SSH access: `ssh gr-droplet` via 1Password SSH agent (`~/.ssh/config` → `Host gr-droplet`).
- No passphrase-less keys on disk — all keys managed by 1Password agent.
- UFW active: only ports 22 and 8080 open.
- Dashboard auth: password-protected actions (`/auth/verify`, SHA-256).
- Secrets in git remotes: rotate the PAT if it appears in git history.

## Deployment

> **GLOBAL RULE: ALWAYS use `make docker-deploy` as the primary deploy method.**
> Never use `make deploy` (systemd/git-pull) unless Docker is unavailable.

- **Deploy:** `make docker-deploy` — builds `linux/amd64` image locally, streams via SSH, restarts container
- **Local run:** `make docker-run` — runs at http://localhost:8080
- **Logs:** `make docker-logs` (local) · `make logs` (remote journalctl)
- **Health:** `make docker-health` — container status + `/ntp/status`
- Server: `104.248.21.29` direct IP, `144.126.244.103` Reserved IP (`Host gr-droplet` in SSH config)
- Container: `ntp-dashboard` · Volume: `ntp_data:/var/lib/ntp` · Port: `8080`
- Data: `/var/lib/ntp/ntp.db` (persisted in Docker named volume, survives redeploys)
