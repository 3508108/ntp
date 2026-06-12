HOST     = gr-droplet
APP_DIR  = /opt/ntp
DATA_DIR = /var/lib/ntp
SERVICE  = ntp-dashboard

# ── deploy (zero-downtime) ────────────────────────────────────────────────────
# 1. push local commits to GitHub
# 2. droplet: pull + pip install + reload (gunicorn graceful, zero downtime)
# 3. record deploy event in DB via /ntp/deploy (with timing + git hash)
.PHONY: deploy
deploy:
	@echo "→ pushing to GitHub..."
	@git push origin main
	@echo "→ deploying to $(HOST)..."
	@ssh $(HOST) 'set -e; \
	  T0=$$(date +%s%3N); \
	  cd $(APP_DIR) && git pull -q && pip3 install -q -r requirements.txt; \
	  systemctl reload $(SERVICE); \
	  sleep 1; \
	  DUR=$$(( $$(date +%s%3N) - $$T0 )); \
	  HASH=$$(git -C $(APP_DIR) rev-parse --short HEAD); \
	  MSG=$$(git -C $(APP_DIR) log -1 --pretty=%s | cut -c1-80); \
	  curl -sf -X POST http://localhost:8080/ntp/deploy \
	    -H "Content-Type: application/json" \
	    -d "{\"duration_ms\":"$$DUR",\"git_hash\":\"$$HASH\",\"message\":\"$$MSG\"}" \
	    > /dev/null; \
	  echo "✓ deployed in $${DUR}ms — zero downtime"'

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
