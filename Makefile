.PHONY: up down build test lint tidy run-control run-tools run-gui run-web eval eval-contract eval-live test-web test-gui test-launcher test-gui-container check

# Workspace root is not a module, so `./...` cannot span modules.
# Build/test each module via its path prefix.
MODS := ./orka_core/... ./orka_middleware/... ./tools_server/... ./orka_control_layer/...

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
	cd orka_core && go mod tidy
	cd orka_middleware && go mod tidy
	cd tools_server && go mod tidy
	cd orka_control_layer && go mod tidy
	go work sync

lint:
	go vet $(MODS)

# ---- run services ----
run-control:
	./scripts/dev.sh control

run-tools:
	./scripts/dev.sh tools

run-gui:
	./scripts/dev.sh gui

run-web:
	./scripts/dev.sh web

# GUI runs from the same private, authenticated Compose configuration as tools.
# noVNC is for administrator-only local debugging. The ordinary UI receives
# private run screenshots over authenticated SSE, not a global live desktop.
gui-sandbox:
	docker compose -f docker-compose.tools.yml up -d --build gui

gui-sandbox-stop:
	docker compose -f docker-compose.tools.yml stop gui

# ---- offline verification (no real model/provider calls) ----
GUI_PYTHON ?= python3
GUI_TEST_IMAGE ?= orka-gui:flash-async

eval: eval-contract

eval-contract:
	go test ./orka_control_layer/cmd/eval ./orka_control_layer/workflow ./orka_control_layer/scheduled_task -count=1

test-web:
	npm --prefix web test

test-gui:
	$(GUI_PYTHON) scripts/gui_contracts.py

test-gui-container:
	docker run --rm --network none --shm-size=512m -e PYTHONDONTWRITEBYTECODE=1 -v "$(CURDIR)/gui_agent:/app:ro" -v "$(CURDIR)/scripts:/checks:ro" -w /app --entrypoint python $(GUI_TEST_IMAGE) /checks/gui_contracts.py --gui-root /app

test-launcher:
	python3 -m unittest discover -s scripts -p 'test_*.py'

check: test test-web test-gui test-launcher

# Explicit opt-in: this target runs real tasks and may incur provider charges.
# EVAL_ARGS carries --url/--tasks/--out/--only; prefer ORKA_EVAL_TOKEN env for auth.
eval-live:
	go run ./orka_control_layer/cmd/eval --live $(EVAL_ARGS)
