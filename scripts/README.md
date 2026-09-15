# Local lifecycle and verification

`./scripts/dev.sh all` starts existing Compose Mongo/Redis infrastructure and
local tools, GUI, control and frontend processes. Linux `/proc` is required.
It loads this repository's `.env` and rejects temporary storage paths.
Run `npm ci --prefix web` first; set `GUI_PYTHON` to a Python interpreter with
`gui_agent/requirements.txt` and Playwright Chromium installed. Services must
use ports matching `CONTROL_ADDR`, `TOOLS_ADDR`, `GUI_PORT`, and `WEB_PORT`.

`control`, `tools`, `gui`, and `web` start one component. Existing managed
processes are retained; use `stop control` then `control` to rebuild deliberately.
`status` reports each PID and whether its process group owns the expected port.
`stop [component]` signals only processes whose PID start time, executable,
command fingerprint and project record still match. An unmanaged process on a
port causes startup to fail. Logs and identity records live in `.run/`.

Stopping does not remove conversations, files, databases, volumes, or Docker
infrastructure. If a component fails during `all`, only components newly started
by that invocation are stopped. Existing services are retained. No startup path
calls a model provider. The authenticated `/system/status` controller reports
protocol readiness separately from liveness and does not execute tools or GUI
model actions.

`make eval` / `make eval-contract` runs fixed local HTTP/SSE fixtures and workflow
and scheduler contracts; no server or model credentials are required. `make
check` also runs all Go tests, frontend contracts, GUI tests, and launcher process
contracts. GUI checks need the GUI requirements and Playwright Chromium. The runner covers
legacy plain functions and unittest classes without pytest. `make test-gui-container`
uses the existing `orka-gui:flash-async` image in a temporary container with no network
and read-only source mounts; override `GUI_TEST_IMAGE` when needed. Override
`GUI_PYTHON=/path/to/python` for a virtual environment. Browser UI tests and builds
also run in `.github/workflows/contracts.yml`.

`make eval-live EVAL_ARGS='--tasks evals/tasks.yaml --out /path/to/scorecard.json'`
explicitly opts in to real tasks and possible model charges. Use environment
variables `ORKA_EVAL_TOKEN` or `ORKA_EVAL_EMAIL` / `ORKA_EVAL_PASSWORD` for login.
Direct invocation of the live command also requires `--live`. All fixture and
live file read/list/delete requests carry the specific evaluation conversation.

The control binary sets `api.BuildVersion` and `api.BuildTime` through linker
flags. `API.SystemStatus` needs an authenticated `GET /system/status` route; it
returns the usual envelope with `version`, `services`, `ready`, and
`model_probe: "not_run"`. Mongo and Redis are required for overall readiness;
configured remote tools/GUI must also respond. Missing optional remote services
are reported as `not_configured`. A GUI health result only proves its health
endpoint is reachable, not that a browser/model task will succeed.
