HOST     = gr-droplet
APP_DIR  = /opt/ntp
DATA_DIR = /var/lib/ntp
SERVICE  = ntp-dashboard
IMAGE    = ntp-dashboard
CONTAINER= ntp-dashboard

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

# ── docker: build ─────────────────────────────────────────────────────────────
.PHONY: docker-build
docker-build:
	@echo "→ building $(IMAGE)..."
	docker build -t $(IMAGE):latest .
	@echo "✓ build complete"
	@docker images $(IMAGE)

# ── docker: run local ─────────────────────────────────────────────────────────
.PHONY: docker-run
docker-run:
	docker compose up -d
	@echo "✓ running at http://localhost:8080"

# ── docker: stop local ────────────────────────────────────────────────────────
.PHONY: docker-stop
docker-stop:
	docker compose down

# ── docker: logs local ────────────────────────────────────────────────────────
.PHONY: docker-logs
docker-logs:
	docker compose logs -f

# ── docker: deploy to droplet (build → stream via SSH → restart) ──────────────
# Does NOT require a registry — streams image directly over SSH.
.PHONY: docker-deploy
docker-deploy:
	@echo "→ building image..."
	@docker build -t $(IMAGE):latest .
	@echo "→ streaming image to $(HOST) (may take ~20s)..."
	@T0=$$(date +%s%3N); \
	 docker save $(IMAGE):latest | gzip | \
	 ssh $(HOST) '\
	   gunzip | docker load && \
	   docker stop $(CONTAINER) 2>/dev/null || true; \
	   docker rm   $(CONTAINER) 2>/dev/null || true; \
	   docker run -d \
	     --name $(CONTAINER) \
	     --restart unless-stopped \
	     -p 8080:8080 \
	     -v ntp_data:/var/lib/ntp \
	     -e NTP_DB=/var/lib/ntp/ntp.db \
	     $(IMAGE):latest'; \
	 DUR=$$(( $$(date +%s%3N) - T0 )); \
	 HASH=$$(git rev-parse --short HEAD); \
	 MSG=$$(git log -1 --pretty=%s | cut -c1-80); \
	 sleep 2; \
	 curl -sf -X POST http://$(HOST):8080/ntp/deploy \
	   -H 'Content-Type: application/json' \
	   -d "{\"duration_ms\":$$DUR,\"git_hash\":\"$$HASH\",\"message\":\"$$MSG (docker)\"}" \
	   > /dev/null || true; \
	 echo "✓ docker-deploy done in $${DUR}ms"

# ── docker: health check ──────────────────────────────────────────────────────
.PHONY: docker-health
docker-health:
	@ssh $(HOST) "docker inspect --format='{{.State.Health.Status}}' $(CONTAINER)"
	@curl -sf http://$(HOST):8080/ntp/status | python3 -m json.tool
