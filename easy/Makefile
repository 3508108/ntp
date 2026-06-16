HOST    = easy-droplet
APP_DIR = /opt/easy
IMAGE   = easy-ntp

.PHONY: deploy
 deploy:
	@echo "→ pushing to GitHub..."
	@git push origin main
	@echo "→ deploying to $(HOST)..."
	@ssh $(HOST) 'set -e; \
	  cd $(APP_DIR) && git pull -q; \
	  docker compose up -d --build; \
	  echo "✓ deployed"'

.PHONY: logs
logs:
	ssh $(HOST) "docker compose -f $(APP_DIR)/docker-compose.yml logs -f"

.PHONY: status
status:
	ssh $(HOST) "docker compose -f $(APP_DIR)/docker-compose.yml ps"
