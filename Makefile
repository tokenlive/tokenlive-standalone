.PHONY: build test run tidy smoke

BINARY ?= bin/tokenlive
CONF ?= config/all-in-one.example.yml
ADMIN_WORKDIR ?= configs/admin
ADMIN_CONFIG ?=
# Empty = auto-detect ../tokenlive-admin/frontend/dist or web/dist
ADMIN_STATIC ?=
DATA_DIR ?= data

build:
	mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/tokenlive

test:
	go test ./...

tidy:
	go mod tidy

# Local all-in-one (requires sibling tokenlive-gateway + tokenlive-admin via go.mod replace)
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

# Start briefly and hit /health on :2525
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
