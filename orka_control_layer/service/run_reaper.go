package service

import (
	"context"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
)

// A run outlives nothing: it is a row in Mongo, a goroutine, and an entry in an
// in-memory map, and only the first of those survives a restart. That mismatch
// is why this file exists — without it every deploy left its in-flight runs
// marked "running" forever, and the platform's own answer to "what is executing
// right now" was pure fiction (measured here: 29 of 29 were stale).
//
// The fix is a liveness signal the storage layer can check on its own. A run
// beats while its process is alive; a beat that stops means the process is gone,
// and a sweeper closes those records out as interrupted.

const (
	// runHeartbeatInterval is how often a live run refreshes its record. Cheap
	// (one indexed update) and far more frequent than the staleness window, so
	// an ordinary hiccup — a slow model call, a paused GC — never looks dead.
	runHeartbeatInterval = 30 * time.Second
	// runStaleAfter is how long a run may go unheard-from before it is presumed
	// dead. Generous relative to the beat: a run must miss ten in a row, which
	// does not happen while the process lives.
	runStaleAfter = 5 * time.Minute
	// reaperInterval is how often the sweeper runs after the initial startup
	// pass, so a run orphaned by a crash mid-uptime is still cleared.
	reaperInterval = 2 * time.Minute
)

// heartbeatRun refreshes runID's liveness until ctx ends. Runs for the lifetime
// of one execution; returns immediately when there is no record to beat for.
func (s *ChatService) heartbeatRun(ctx context.Context, runID string) {
	if runID == "" || s.Msg == nil || s.Msg.Store == nil {
		return
	}
	t := time.NewTicker(runHeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// A cancelled request context must not stop the beat from being
			// written, but the run itself is ending anyway, so best-effort with a
			// short independent timeout is right.
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Msg.Store.TouchRun(c, runID)
			cancel()
		}
	}
}

// StartRunReaper closes out runs orphaned by a previous process and keeps
// sweeping while ctx lives. Call once at startup, before serving: the first pass
// is what makes the run list trustworthy again after a restart.
func (s *ChatService) StartRunReaper(ctx context.Context) {
	if s.Msg == nil || s.Msg.Store == nil {
		return
	}
	sweep := func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ids, err := s.Msg.Store.ReapStaleRuns(c, runStaleAfter)
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("run reaper failed", "err", err)
			}
			return
		}
		if len(ids) == 0 {
			return
		}
		// A crashed run never reached the code that flags itself resumable, so
		// the reaper does it: if a transcript survived, continuing is worth far
		// more here than anywhere else — this is the case where an entire
		// multi-hour run would otherwise be redone from nothing.
		resumable := 0
		for _, id := range ids {
			f := loadJournal(s.Cfg.Storage.BaseStoragePath, id)
			if f == nil || len(f.Messages) < resumeWorthwhileSteps {
				dropJournal(s.Cfg.Storage.BaseStoragePath, id)
				continue
			}
			if s.Msg.Store.SetRunResumable(c, id, len(f.Messages)) == nil {
				resumable++
			}
		}
		if s.Log != nil {
			s.Log.Info("reaped orphaned runs", "count", len(ids), "status", db.RunInterrupted, "resumable", resumable)
		}
	}
	sweep() // startup pass: clear the previous process's residue
	go func() {
		t := time.NewTicker(reaperInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()
}
