# Project Rules — ntp-droplet

## Credential management

> Global rule applies: **always save to 1Password first** (vault: Personal).
> Central `.env` is at `~/Documents/coding/.env` — source it in any project shell.

Credentials used by this project, all stored in 1Password (Personal vault):

| Variable | 1Password item | Notes |
|---|---|---|
| `NTP_DROPLET_HOST` | `ntp-droplet (64.226.102.81)` (uxxanuajptep2nrgna5zjvviba) | Server IP — not a secret |
| `github` / `github_classic` | `GitHub Personal Access Token`, `GitHub Classic PAT` | GitHub PATs for CI/CD |
| `vultr` | `Vultr API Token` (u6pb6qprdcap3rozagiz6gmi7q) | Vultr API — server provisioning |

Rotation checklist:
1. Update value in 1Password
2. Update `~/Documents/coding/.env`
3. Update GitHub Secret via repo Settings → Secrets

Never commit `.env` or any raw credential to git.
`.gitignore` already covers: `.env`.

## Security

- SSH access: root@64.226.102.81 via 1Password SSH agent (`~/.ssh/config` → `Host ntp-droplet`).
- No passphrase-less keys on disk — all keys managed by 1Password agent.
- Secrets in git remotes: rotate the PAT if it appears in git history.

## Deployment

- Build: `docker build` via `Makefile`
- Deploy: `make deploy` or via GitHub Actions
- Server: `64.226.102.81` (`Host ntp-droplet` in SSH config)
