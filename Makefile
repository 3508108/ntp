# ── Go-білд ──────────────────────────────────────────────────────────────────
BIN := bin/ntp-dashboard

.PHONY: build
build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/ntp
	@echo "✓ build complete: $(BIN)"
	@ls -lh $(BIN)

.PHONY: run
run: build
	./$(BIN)

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: clean
clean:
	rm -rf bin

# ── деплой (zero-downtime) ───────────────────────────────────────────────────
HOST     := gr-droplet
APP_DIR  := /opt/ntp
DATA_DIR := /var/lib/ntp
SERVICE  := ntp-dashboard
IMAGE    := ntp-dashboard
CONTAINER := ntp-dashboard

.PHONY: deploy
deploy: build
	@echo "→ pushing to GitHub..."
	@git push origin main
	@echo "→ deploying to $(APP_DIR) on $(HOST)..."
	@ssh $(HOST) 'set -e; \
	  T0=$$(date +%s%3N); \
	  cd $(APP_DIR) && git pull -q && (/usr/local/go/bin/go build -o /opt/ntp/bin/ntp-dashboard ./cmd/ntp); \
	  systemctl reload $(SERVICE); \
	  sleep 1; \
	  DUR=$$(( $$(date +%s%3N) - $$T0 )); \
	  HASH=$$(git -C $(APP_DIR) rev-parse --short HEAD); \
	  MSG=$$(git -C $(APP_DIR) log -1 --pretty=%s | cut -c1-80); \
	  curl -sf -X POST http://localhost:8080/ntp/deploy \
	    -H "Content-Type: application/json" \
	    -d "{\"duration_ms":$$DUR",\"git_hash\":\""$$HASH"\",\"message\":\""$$MSG"\"}" \
	    > /dev/null; \
	  echo "✓ deployed in $${DUR}ms — zero downtime"'

.PHONY: logs
logs:
	ssh $(HOST) "journalctl -u $(SERVICE) -f --no-pager"

.PHONY: status
status:
	ssh $(HOST) "systemctl status $(SERVICE) --no-pager && echo '' && ls -lh $(DATA_DIR)/"

.PHONY: db
db:
	ssh $(HOST) "sqlite3 $(DATA_DIR)/ntp.db 'SELECT COUNT(*),MIN(ts),MAX(ts) FROM ntp_samples;'"

.PHONY: ssh
ssh:
	ssh $(HOST)

.PHONY: restart
restart:
	ssh $(HOST) "systemctl restart $(SERVICE)"

.PHONY: docker-build
docker-build:
	@echo "→ building $(IMAGE) (linux/amd64)..."
	docker build --platform linux/amd64 -t $(IMAGE):latest .
	@echo "✓ build complete"
	@docker images $(IMAGE)

.PHONY: docker-run
docker-run:
	docker compose up -d
	@echo "✓ running at http://localhost:8080"

.PHONY: docker-stop
docker-stop:
	docker compose down

.PHONY: docker-logs
docker-logs:
	docker compose logs -f

.PHONY: docker-deploy
docker-deploy: docker-build
	@docker save $(IMAGE):latest | gzip | \
	  ssh $(HOST) 'set -e; \
	    gunzip | docker load; \
	    systemctl stop $(SERVICE) 2>/dev/null || true; \
	    docker stop $(CONTAINER) 2>/dev/null || true; \
	    docker rm   $(CONTAINER) 2>/dev/null || true; \
	    docker run -d \
	      --name $(CONTAINER) \
	      --restart unless-stopped \
	      -p 8080:8080 \
	      -v ntp_data:/var/lib/ntp \
	      -e NTP_DB=/var/lib/ntp/ntp.db \
	      $(IMAGE):latest; \
	    echo "✓ container started"'

.PHONY: docker-health
docker-health:
	@ssh $(HOST) "docker inspect --format='{{.State.Health.Status}}' $(CONTAINER)" 2>/dev/null || true
	@curl -sf http://$(HOST):8080/ntp/status | python3 -m json.tool 2>/dev/null || true
