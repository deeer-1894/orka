package browsertool

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"
)

// The grace period catches ordinary deferred click/SPA handlers. The quiet
// window is bounded evidence, not a promise that a site has no future timers.
const observationGrace = 600 * time.Millisecond
const observationQuiet = 200 * time.Millisecond
const observationWindow = 2500 * time.Millisecond

//go:embed scripts/settle.js
var settleScript string

type pageState struct {
	Loading  bool  `json:"loading"`
	Revision int64 `json:"revision"`
}

type observationStamp struct {
	pageState
	Epoch int64
	DOM   int64
	Ready bool
}

func readPageState(ctx context.Context, s Session) (pageState, error) {
	var state pageState
	err := s.Lease.Execute(ctx, "Orka.getPageState", nil, &state)
	return state, err
}

func readObservationStamp(ctx context.Context, s Session) (observationStamp, error) {
	state, err := readPageState(ctx, s)
	stamp := observationStamp{pageState: state, Epoch: s.Lease.Info().PageEpoch}
	if err != nil || state.Loading {
		return stamp, err
	}
	var response runtimeReply
	err = s.Lease.Execute(ctx, "Orka.observe", map[string]any{
		"operation": "settle",
	}, &response)
	if err != nil {
		return stamp, err
	}
	if len(response.ExceptionDetails) > 0 && string(response.ExceptionDetails) != "null" {
		return stamp, NewActionError("observation_failed", "Page readiness could not be observed.")
	}
	var value struct {
		Revision int64 `json:"revision"`
		Ready    bool  `json:"ready"`
	}
	if err = json.Unmarshal(response.Result.Value, &value); err != nil {
		return stamp, NewActionError("observation_failed", "Page readiness reply was invalid.")
	}
	stamp.DOM, stamp.Ready = value.Revision, value.Ready
	return stamp, nil
}

func createCurrentWorld(ctx context.Context, s *Session) error {
	for attempt := 0; attempt < 3; attempt++ {
		id, err := createWorld(ctx, s.Lease)
		if err == nil {
			s.ContextID = id
			return nil
		}
		if !observationContextLost(err) {
			return err
		}
		if err = observationPause(ctx); err != nil {
			return err
		}
	}
	return observationFailure(false)
}

func observationContextLost(err error) bool {
	if err == nil {
		return false
	}
	code := actionError(err).Code
	return code == "context_lost" || code == "stale_ref"
}

func observationFailure(afterAction bool) error {
	if afterAction {
		return NewActionError("observation_failed", "The action was acknowledged, but a stable final page observation is unavailable. Inspect with snapshot or wait; do not repeat the action to recover its observation.")
	}
	return NewActionError("observation_failed", "The current page could not be observed reliably. Take a new snapshot or wait for a specific page condition.")
}

func observationPause(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// observeCurrent is the only retry boundary. It retries world acquisition and
// observation after document races, never the caller's navigation/input/script.
// Failed attempts use a private receipt so obsolete refs cannot escape.
func observeCurrent(ctx context.Context, s *Session, result *Result, req Request, settle bool) error {
	// Bound CDP reads themselves, not only the interval between retries. A
	// stalled renderer must not consume the whole action's 45-second timeout.
	ctx, cancel := context.WithTimeout(ctx, observationWindow)
	defer cancel()
	started := time.Now()
	quietSince := started
	var previous observationStamp
	for time.Since(started) < observationWindow {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Chromium may defer world creation until a provisional load commits.
		// Inspect native loading first, without entering that document's JS.
		state, err := readPageState(ctx, *s)
		if err != nil {
			return err
		}
		if state.Loading {
			quietSince = time.Now()
			if err = observationPause(ctx); err != nil {
				return err
			}
			continue
		}
		var before observationStamp
		if err == nil {
			before, err = readObservationStamp(ctx, *s)
		}
		if err == nil {
			if before != previous {
				quietSince = time.Now()
				previous = before
			}
			if before.Ready && !before.Loading && (!settle || time.Since(started) >= observationGrace && time.Since(quietSince) >= observationQuiet) {
				var candidate Result
				err = observe(ctx, *s, &candidate, req)
				if err == nil {
					err = compactObservation(ctx, s, candidate.Snapshot, req.observation.normalized())
				}
				if err == nil {
					var after observationStamp
					after, err = readObservationStamp(ctx, *s)
					if err == nil && before == after {
						_, err = page(ctx, *s, "commit_observation", Request{SnapshotID: candidate.Snapshot.ID})
						if err != nil {
							return err
						}
						result.URL, result.Title, result.Snapshot = candidate.URL, candidate.Title, candidate.Snapshot
						result.Change, result.Progress = candidate.Change, candidate.Progress
						result.PageEpoch = after.Epoch
						return nil
					}
					if err == nil {
						// Prefer a self-contained replacement after a raced candidate.
						req.View = "full"
					}
				}
			}
		}
		if err != nil {
			if !observationContextLost(err) {
				return err
			}
			quietSince = time.Now()
		}
		if err := observationPause(ctx); err != nil {
			return err
		}
	}
	return observationFailure(settle)
}

// waitCurrent retries only its read side after navigation destroys the world.
// A user's stale target remains stale: it is never re-resolved by name or CSS.
func waitCurrent(ctx context.Context, s *Session, req Request) error {
	for {
		err := waitFor(ctx, *s, req)
		if !observationContextLost(err) || req.Ref != "" {
			return err
		}
		if err = observationPause(ctx); err != nil {
			return err
		}
	}
}
