.PHONY: up down build test lint tidy run-control run-tools run-gui eval

# Workspace root is not a module, so `./...` cannot span modules.
# Build/test each module via its path prefix.
MODS := ./cavis_core/... ./cavis_middleware/... ./tools_server/... ./cavis_control_layer/...

# ---- local infra ----
up:
	docker compose up -d
	@echo "waiting for redis/mongo healthchecks..."
	@docker compose ps

down:
	docker compose down

# ---- go workspace ----
build:
	go work sync
	go build $(MODS)

test:
	go test $(MODS)

tidy:
	cd cavis_core && go mod tidy
	cd cavis_middleware && go mod tidy
	cd tools_server && go mod tidy
	cd cavis_control_layer && go mod tidy
	go work sync

lint:
	go vet $(MODS)

# ---- run services ----
run-control:
	go run ./cavis_control_layer

run-tools:
	go run ./tools_server

run-gui:
	cd gui_agent && python3 main.py

# ---- eval (Phase 8) ----
eval:
	cd cavis_control_layer && go test ./eval/... -run TestReplay -v
