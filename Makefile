.PHONY: build test run tidy smoke package brew-install brew-uninstall

BINARY ?= bin/tokenlive
CONF ?= config/all-in-one.example.yml
ADMIN_WORKDIR ?= configs/admin
ADMIN_CONFIG ?=
ADMIN_STATIC ?=
DATA_DIR ?= data
VERSION ?= 0.1.0-dev

build:
	mkdir -p bin
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/tokenlive

test:
	go test ./...

tidy:
	go mod tidy

run: build
	mkdir -p $(DATA_DIR)
	./$(BINARY) \
		-conf $(CONF) \
		-data-dir $(DATA_DIR) \
		-admin-workdir $(ADMIN_WORKDIR) \
		$(if $(ADMIN_CONFIG),-admin-config $(ADMIN_CONFIG),) \
		$(if $(ADMIN_STATIC),-admin-static $(ADMIN_STATIC),)

smoke-validate:
	@go test ./internal/assemble/ -run TestValidateAllInOne -count=1
	@echo "validate ok"

smoke: build
	mkdir -p $(DATA_DIR)
	@./$(BINARY) -conf $(CONF) -data-dir $(DATA_DIR) -admin-workdir $(ADMIN_WORKDIR) $(if $(ADMIN_CONFIG),-admin-config $(ADMIN_CONFIG),) > /tmp/tokenlive-smoke.log 2>&1 & \
		pid=$$!; \
		trap 'kill $$pid 2>/dev/null; wait $$pid 2>/dev/null' EXIT; \
		ok=0; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do \
			if curl -sf http://127.0.0.1:2525/health >/tmp/tokenlive-health.json 2>/dev/null; then \
				ok=1; break; \
			fi; \
			if ! kill -0 $$pid 2>/dev/null; then break; fi; \
			sleep 1; \
		done; \
		if [ "$$ok" != "1" ]; then echo "smoke: health check failed"; tail -40 /tmp/tokenlive-smoke.log; exit 1; fi; \
		echo "smoke: $$(cat /tmp/tokenlive-health.json)"

# Full release layout under dist/ (binary + admin share + web + etc)
package:
	VERSION=$(VERSION) ./scripts/package-release.sh

# Local Homebrew-style install + LaunchAgent / brew services
brew-install:
	./scripts/brew-install-local.sh

brew-uninstall:
	./scripts/brew-uninstall-local.sh

brew-start:
	@brew services start tokenlive 2>/dev/null || tokenlive-start

brew-stop:
	@brew services stop tokenlive 2>/dev/null || tokenlive-stop
