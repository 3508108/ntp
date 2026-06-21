GO ?= go
DOCKER ?= docker
COMPOSE ?= docker compose

HOST ?= easy-droplet
APP_DIR ?= /opt/easy
SERVICE ?= easy

BIN_LOCAL ?= easy
BIN_LINUX ?= easy-linux
IMAGE ?= ntp-easy:local

.PHONY: build
build:
	$(GO) build -o $(BIN_LOCAL) .

.PHONY: build-linux
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(BIN_LINUX) .

.PHONY: run
run:
	$(GO) run .

.PHONY: test
test:
	$(GO) test ./...

.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(IMAGE) .

.PHONY: docker-run
docker-run: docker-build
	$(DOCKER) run --rm -p $${EASY_PORT:-8080}:8080 -e EASY_DB=/data/easy.db -e EASY_INTERVAL=$${EASY_INTERVAL:-10s} -v ntp-easy-data:/data $(IMAGE)

.PHONY: docker-up
docker-up:
	$(COMPOSE) up --build

.PHONY: docker-down
docker-down:
	$(COMPOSE) down

.PHONY: deploy
deploy: build-linux
	scp $(BIN_LINUX) $(HOST):$(APP_DIR)/easy
	ssh $(HOST) 'chmod +x $(APP_DIR)/easy && systemctl restart $(SERVICE) && systemctl status $(SERVICE) --no-pager'

.PHONY: logs
logs:
	ssh $(HOST) 'journalctl -u $(SERVICE) -f --no-pager'

.PHONY: status
status:
	ssh $(HOST) 'systemctl status $(SERVICE) --no-pager'
