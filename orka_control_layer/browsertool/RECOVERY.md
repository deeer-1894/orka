# Browser observation and navigation recovery

The engine owns action semantics and bounded observation. Python owns the page,
shared visible queue, isolated worlds and cleanup. A failed receipt never causes
the engine to replay an input, navigation, form field or user script.

## Private protocol

- `Orka.getPageState {}` reads cached main-frame loading/revision events. It does
  not call into the renderer: even `Page.getFrameTree` may block before commit.
- `Orka.observe {operation, request}` runs only fixed `snapshot`, `settle`, `wait`
  or `commit_observation` helpers. `Orka.act` accepts only fixed action operations.
- These helpers run in `orka-observer`, whose context and object handles are never
  granted to the generic CDP client. User evaluate stays in `orka-browser`.
  Neither a caller-supplied script nor a `readonly` flag can grant read privileges.
- `commit_observation` is sent only after the engine accepts a stable candidate.
  A cancelled lease restores its previous baseline. Unconfirmed finalization
  invalidates observation scope without closing the page.
- After receiving `released`, Go sends `observation_ack`. Until Python receives
  this receipt, a later observation resets its baseline and returns full. Lost
  release/receipt messages therefore cannot make a later delta depend on an
  undelivered candidate. Baseline reset preserves input redactions and DOM refs.
  This confirms delivery to the control transport, not consumption by a model
  or a person; those later delivery boundaries belong to the service layer.

The public tool parameters are unchanged. `observation_failed` distinguishes an
unavailable observation after an acknowledged action from an unconfirmed action
(`outcome_unknown`). Neither error instructs callers to repeat a mutation.

## Recovery boundaries

Native navigation sends remain owned by the lease after cancellation. Cleanup
stops loading, confirms the navigation task settled and checks loading events
before permitting page reuse. A click may start navigation after its input was
acknowledged; an unfinished load is also stopped before releasing that lease.
Failure to confirm stopping/settlement still closes the page. Unconfirmed page
closure quarantines the runtime. Repeated cancellation cannot interrupt cleanup.

Fixed read failures and remote-object release failures do not destroy the page.
Arbitrary JS and unconfirmed input retain the conservative close/quarantine
behavior. A generic script that destroys its context may already have changed
the site; the engine never treats that as permission to execute it again.

Action observations allow a 600 ms initial grace, require 200 ms of quiet DOM and
navigation state, and stop trying after 2.5 s (also bounded by the caller deadline).
Observations are bracketed by state checks and the final release epoch. This is
bounded evidence, not a guarantee against arbitrary future timers or completion
of a site's business operation. Longer workflows should use explicit wait
conditions. No tabs manager or native back/forward actions are added here.

## Canonical helpers and deployment

Go-owned files in `scripts/` remain canonical. Before building/deploying the GUI:

```sh
python3 gui_agent/scripts/sync_browser_helpers.py
go test ./orka_control_layer/browsertool -run TestFixedBrowserHelperBundleMatchesCanonicalSources
```

`gui_agent/service/browser_helpers.json` is generated, not independently edited.
No Python dependency changes are required. The GUI Dockerfile already copies it.

For the existing unmounted `orka-gui` container, the final runtime update consists
of these five files, each copied to the corresponding `/app/service/` path:

1. `gui_agent/service/browser_runtime.py`
2. `gui_agent/service/browser_bridge.py`
3. `gui_agent/service/browser_commands.py`
4. `gui_agent/service/browser_observation.py`
5. `gui_agent/service/browser_helpers.json`

The coordinator must arrange restart after active work ends: pages and contexts
are in-memory and do not survive replacement. Rebuild the Go control binary too;
its connector/engine use the new private methods and delivery receipt. Updating
GUI first is compatible with the older generic CDP engine; the new engine requires
the new GUI. For durable deployment, rebuild the GUI image from `gui_agent/` with
the generated bundle included; copying into a running container alone does not
update its base image.

## Isolated verification

`gui_agent/tests/browser_test_server.py` starts a disposable bridge on loopback
8765 with a literal fixture-only token and no production server/config imports.
Set `BROWSER_TEST_HEADFUL=1` to use an isolated Xvfb display and window manager,
matching production. Older integration fixtures also need
`host.docker.internal` mapped to the test host. Go selects the bridge through
`ORKA_BROWSER_TEST_WS` and `ORKA_BROWSER_TEST_TOKEN`; absent endpoint tests skip.

Regression coverage is in `recovery*_test.go`, `connectors/browser_recovery_test.go`,
`gui_agent/tests/test_browser_recovery.py` and `test_fixed_observation.py`. The
original integration contracts also exercise inputs/refs, frames, preview,
screenshots, authenticated downloads, size limits, isolation and cancellation.
The coordinator's actual right-panel long-task test remains a separate acceptance
step; isolated browser tests do not replace it.
