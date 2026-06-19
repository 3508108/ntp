GO ?= go

HOST ?= easy-droplet
APP_DIR ?= /opt/easy
SERVICE ?= easy

BIN_LOCAL ?= easy
BIN_LINUX ?= easy-linux

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
