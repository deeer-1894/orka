package service

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
)

// eino_checkpoint.go — a CheckPointStore for ADK interrupt/resume (P3).
//
// eino's Runner persists the whole run state at an interrupt point and restores
// it on Resume. That is what turns human-in-the-loop from "a goroutine parked on
// a channel for five minutes, lost on restart" into "the run is on disk until
// someone answers".
//
// File-backed rather than Redis-backed on purpose: it is the same persistence
// story as the factor library and the confirm grants, it needs no extra wiring
// into ChatService, and a checkpoint that outlives a control-plane restart is
// exactly the point.

// checkpointTTL bounds how long a paused run is resumable. Beyond this the
// checkpoint is swept: a human who never answered is not going to.
const checkpointTTL = 24 * time.Hour

// The Runner serializes a checkpoint with gob, and our interrupt/resume payloads
// travel inside it as interface values — gob refuses to encode those unless the
// concrete types are registered. Without this, pausing for approval fails with
// "gob: type not registered for interface".
func init() {
	gob.Register(ConfirmInterrupt{})
	gob.Register(confirmDecision{})
}

type fileCheckpointStore struct{ dir string }

// newCheckpointStore returns a store rooted under the storage path, or nil when
// storage is unconfigured (headless/test runs then keep the blocking gate).
func newCheckpointStore(baseStorage string) adk.CheckPointStore {
	if baseStorage == "" {
		return nil
	}
	dir := filepath.Join(baseStorage, ".orka_checkpoints")
	if os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	s := &fileCheckpointStore{dir: dir}
	s.sweep()
	return s
}

// path maps a checkpoint id to a file, keeping it inside dir (ids are
// conversation ids, but never trust them with a separator).
func (s *fileCheckpointStore) path(id string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(id)
	return filepath.Join(s.dir, safe+".ckpt")
}

func (s *fileCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // "no checkpoint" is not an error
		}
		return nil, false, err
	}
	return b, true, nil
}

func (s *fileCheckpointStore) Set(_ context.Context, id string, cp []byte) error {
	return os.WriteFile(s.path(id), cp, 0o600)
}

// sweep removes checkpoints past the TTL so an abandoned pause doesn't linger.
func (s *fileCheckpointStore) sweep() {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-checkpointTTL)
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, e.Name()))
	}
}

// drop deletes a checkpoint once its run has been resumed to completion.
func (s *fileCheckpointStore) drop(id string) { _ = os.Remove(s.path(id)) }

// checkpointStore lazily builds (once) the store used for interrupt/resume.
func (s *ChatService) checkpointStore() adk.CheckPointStore {
	s.ckptInit.Do(func() { s.ckpt = newCheckpointStore(s.Cfg.Storage.BaseStoragePath) })
	return s.ckpt
}

// --- context plumbing --------------------------------------------------------

// ckptStoreKey carries the checkpoint store down to StreamEinoRun without
// threading it through every signature.
type ckptStoreKey struct{}

func withCheckpointStore(ctx context.Context, s adk.CheckPointStore) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, ckptStoreKey{}, s)
}

func checkpointStoreFrom(ctx context.Context) adk.CheckPointStore {
	if s, ok := ctx.Value(ckptStoreKey{}).(adk.CheckPointStore); ok {
		return s
	}
	return nil
}

// isInterruptErr reports whether err is an ADK interrupt signal (control flow),
// as opposed to a genuine tool failure. compose.ExtractInterruptInfo only matches
// the graph-level interruptError, so a signal raised inside a tool needs this.
func isInterruptErr(err error) bool {
	if err == nil {
		return false
	}
	var sig *adk.InterruptSignal
	if errors.As(err, &sig) {
		return true
	}
	_, ok := compose.ExtractInterruptInfo(err)
	return ok
}

// varPendingConfirm is the RunContext var carrying an interrupted run's confirm
// details up to Run, which persists them so /chat/confirm can rebuild the run.
const varPendingConfirm = "pending_confirm"

// confirmFromInterrupt digs the ConfirmInterrupt payload and its resume address
// out of an ADK interrupt. Returns ok=false for interrupts we didn't raise
// (e.g. clarify), which are handled by their own path.
func confirmFromInterrupt(info *adk.InterruptInfo) (ConfirmInterrupt, string, bool) {
	if info == nil {
		return ConfirmInterrupt{}, "", false
	}
	for _, ic := range info.InterruptContexts {
		if ic == nil || !ic.IsRootCause {
			continue
		}
		if ci, ok := ic.Info.(ConfirmInterrupt); ok {
			return ci, ic.ID, true
		}
		if ci, ok := ic.Info.(*ConfirmInterrupt); ok && ci != nil {
			return *ci, ic.ID, true
		}
	}
	return ConfirmInterrupt{}, "", false
}

// --- resume plumbing ---------------------------------------------------------

// resumeKey carries the target address + decision for a resumed run down to
// StreamEinoRun, which turns it into Runner.ResumeWithParams.
type resumeKey struct{}

type resumeSpec struct {
	Target string // InterruptCtx.ID of the paused tool call
	Data   any    // decision handed to that tool via GetResumeContext
}

func withResume(ctx context.Context, target string, data any) context.Context {
	return context.WithValue(ctx, resumeKey{}, &resumeSpec{Target: target, Data: data})
}

func resumeFrom(ctx context.Context) *resumeSpec {
	if r, ok := ctx.Value(resumeKey{}).(*resumeSpec); ok {
		return r
	}
	return nil
}

// pausedRun is what must be remembered to rebuild an interrupted run later —
// including after a control-plane restart, which is the whole point of moving
// off a parked goroutine.
type pausedRun struct {
	Request ChatRunRequest `json:"request"`
	Target  string         `json:"target"` // interrupt address to resume
	Tool    string         `json:"tool"`
	Summary string         `json:"summary"`
	SavedAt int64          `json:"saved_at"`
}

func pausedPath(baseStorage, convID string) string {
	if baseStorage == "" || convID == "" {
		return ""
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(convID)
	return filepath.Join(baseStorage, ".orka_checkpoints", safe+".paused.json")
}

func savePausedRun(baseStorage string, p pausedRun) {
	path := pausedPath(baseStorage, p.Request.ConversationID)
	if path == "" {
		return
	}
	p.SavedAt = time.Now().UnixMilli()
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o600)
}

func loadPausedRun(baseStorage, convID string) (pausedRun, bool) {
	var p pausedRun
	path := pausedPath(baseStorage, convID)
	if path == "" {
		return p, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return p, false
	}
	if json.Unmarshal(b, &p) != nil || p.Target == "" {
		return p, false
	}
	return p, true
}

func dropPausedRun(baseStorage, convID string) {
	if path := pausedPath(baseStorage, convID); path != "" {
		_ = os.Remove(path)
	}
}
