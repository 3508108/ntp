HOST     = gr-droplet
APP_DIR  = /opt/ntp
DATA_DIR = /var/lib/ntp
SERVICE  = ntp-dashboard

# ── deploy (zero-downtime) ────────────────────────────────────────────────────
# 1. push local commits to GitHub
# 2. droplet pulls latest code
# 3. install any new deps (quiet)
# 4. systemctl reload  ← gunicorn graceful: old workers finish, new ones start
.PHONY: deploy
deploy:
	@echo "→ pushing to GitHub..."
	@git push origin main
	@echo "→ deploying to $(HOST)..."
	@ssh $(HOST) " \
	  cd $(APP_DIR) && \
	  git pull -q && \
	  pip3 install -q -r requirements.txt && \
	  systemctl reload $(SERVICE) && \
	  echo '✓ reloaded — zero downtime' \
	"

# ── logs ─────────────────────────────────────────────────────────────────────
.PHONY: logs
logs:
	ssh $(HOST) "journalctl -u $(SERVICE) -f --no-pager"

# ── status ───────────────────────────────────────────────────────────────────
.PHONY: status
status:
	ssh $(HOST) "systemctl status $(SERVICE) --no-pager && echo '' && ls -lh $(DATA_DIR)/"

# ── db ───────────────────────────────────────────────────────────────────────
.PHONY: db
db:
	ssh $(HOST) "sqlite3 $(DATA_DIR)/ntp.db 'SELECT COUNT(*),MIN(ts),MAX(ts) FROM ntp_samples;'"

# ── ssh ───────────────────────────────────────────────────────────────────────
.PHONY: ssh
ssh:
	ssh $(HOST)

# ── full restart (only when needed) ──────────────────────────────────────────
.PHONY: restart
restart:
	ssh $(HOST) "systemctl restart $(SERVICE)"
