package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

// A long run's transcript is the only thing standing between a transient blip
// and hours of lost work. Measured on this deployment: a run died after 10,988
// seconds and 358k tokens to a "stream read: unexpected EOF"; three more lost
// 100–160k tokens the same way. Every one of those had a complete, valid
// transcript right up to the moment it died — and threw it away.
//
// eino checkpoints only at an interrupt or a Stop(), which is the right
// behaviour for those cases and no help at all for these: an EOF from the
// provider, a tool that dies mid-stream, a process that is killed. So the
// transcript is journaled here, on the boundary that matters — a completed tool
// call, which is where real work (a written file, a finished search) lands.
//
// The journal is not a checkpoint of the agent's internal machinery; it is the
// conversation. Resuming means building a fresh agent and handing it what the
// previous attempt had already established, which is exactly what a model needs
// to carry on and is robust to the agent graph changing underneath it.

// runJournal accumulates one run's transcript and flushes it to durable storage.
// Safe for concurrent use: sub-agent events arrive on their own goroutines.
type runJournal struct {
	dir   string
	runID string

	mu    sync.Mutex
	seed  []*schema.Message // the input the run started from
	msgs  []*schema.Message // assistant / tool messages it produced
	dirty bool
}

// journalFile is the on-disk shape. JSON rather than gob so a stuck run can be
// inspected by hand, which matters for something that only exists to be read
// after something went wrong.
type journalFile struct {
	RunID     string            `json:"run_id"`
	UpdatedAt int64             `json:"updated_at"`
	Seed      []*schema.Message `json:"seed"`
	Messages  []*schema.Message `json:"messages"`
}

// newRunJournal opens a journal for a run. Returns nil when storage is
// unconfigured (tests, headless) — every method tolerates a nil receiver, so
// journaling simply does not happen rather than becoming a second failure mode.
func newRunJournal(baseStorage, runID string, seed []*schema.Message) *runJournal {
	if baseStorage == "" || runID == "" {
		return nil
	}
	dir := filepath.Join(baseStorage, ".orka_journals")
	if os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	return &runJournal{dir: dir, runID: runID, seed: seed}
}

func (j *runJournal) path() string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(j.runID)
	return filepath.Join(j.dir, safe+".json")
}

// setSeed records the input the run actually started from — on a resume that is
// the previous attempt's transcript, so the journal stays a complete record of
// the conversation rather than only the newest leg of it.
func (j *runJournal) setSeed(seed []*schema.Message) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.seed = seed
	j.dirty = true
	j.mu.Unlock()
}

// append records one authoritative message. Cheap and in-memory; flush is what
// touches the disk.
func (j *runJournal) append(m *schema.Message) {
	if j == nil || m == nil {
		return
	}
	j.mu.Lock()
	j.msgs = append(j.msgs, m)
	j.dirty = true
	j.mu.Unlock()
}

// flush writes the transcript if it changed. Called after each completed tool
// call: that is both the natural durability boundary (the work it describes has
// already happened) and infrequent enough — tens of times per run — that a small
// synchronous write costs nothing next to a model call.
func (j *runJournal) flush() {
	if j == nil {
		return
	}
	j.mu.Lock()
	if !j.dirty {
		j.mu.Unlock()
		return
	}
	f := journalFile{
		RunID:     j.runID,
		UpdatedAt: time.Now().UnixMilli(),
		Seed:      append([]*schema.Message(nil), j.seed...),
		Messages:  append([]*schema.Message(nil), j.msgs...),
	}
	j.dirty = false
	j.mu.Unlock()

	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	// Write-then-rename so a crash mid-write cannot leave a truncated journal —
	// the one file whose job is to survive a crash.
	tmp := j.path() + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, j.path())
	}
}

// steps reports how many messages the run produced, for telling the user what
// resuming would preserve.
func (j *runJournal) steps() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.msgs)
}

// discard removes the journal. Called when a run ends in a state nobody would
// resume from — success, or a failure with nothing worth keeping.
func (j *runJournal) discard() {
	if j == nil {
		return
	}
	_ = os.Remove(j.path())
	_ = os.Remove(j.path() + ".tmp")
}

// loadJournal reads a run's transcript back. Returns nil when there is nothing
// to resume from.
func loadJournal(baseStorage, runID string) *journalFile {
	if baseStorage == "" || runID == "" {
		return nil
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(runID)
	data, err := os.ReadFile(filepath.Join(baseStorage, ".orka_journals", safe+".json"))
	if err != nil {
		return nil
	}
	var f journalFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return &f
}

// dropJournal removes a run's journal from outside the run (e.g. once it has
// been resumed successfully).
func dropJournal(baseStorage, runID string) {
	if baseStorage == "" || runID == "" {
		return
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(runID)
	base := filepath.Join(baseStorage, ".orka_journals", safe+".json")
	_ = os.Remove(base)
	_ = os.Remove(base + ".tmp")
}

// resumeMessages rebuilds a model-ready conversation from a journal.
//
// The critical part is the trailing sanitization. A run usually dies DURING a
// tool call — the assistant asked for one and the result never arrived — and
// providers reject a conversation whose tool_calls have no matching responses
// with a hard 400. Replaying such a transcript verbatim would make every resume
// fail, and fail in a way that looks like the resume feature is broken rather
// than the input. So any trailing assistant turn whose tool calls are unanswered
// is dropped: that request simply never happened, and the resumed agent is free
// to make it again.
func resumeMessages(f *journalFile) []*schema.Message {
	if f == nil {
		return nil
	}
	msgs := append(append([]*schema.Message(nil), f.Seed...), f.Messages...)
	answered := map[string]bool{}
	for _, m := range msgs {
		if m != nil && m.Role == schema.Tool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	// Walk back from the end, dropping messages until the transcript is coherent.
	for len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		if last == nil {
			msgs = msgs[:len(msgs)-1]
			continue
		}
		if last.Role == schema.Assistant && len(last.ToolCalls) > 0 {
			complete := true
			for _, tc := range last.ToolCalls {
				if !answered[tc.ID] {
					complete = false
					break
				}
			}
			if !complete {
				msgs = msgs[:len(msgs)-1]
				continue
			}
		}
		break
	}
	return msgs
}

// resumeNotice tells the resumed agent what happened. Without it the model sees
// a conversation that simply stops mid-thought and tends to either restart from
// the beginning — defeating the point — or apologize for an interruption the
// user never saw.
func resumeNotice(reason string, steps int) *schema.Message {
	why := "上一次运行意外中断"
	switch reason {
	case "cancelled":
		why = "上一次运行被用户手动停止"
	case "interrupted":
		why = "上一次运行因服务重启而中断"
	}
	return schema.UserMessage("[系统] " + why + "(已完成 " + itoa(steps) + " 步,记录见上文)。\n" +
		"请从中断处继续,不要从头重做已经完成的工作。先简短说明你将接着做什么,然后继续执行。")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// resumeWorthwhileSteps is the transcript length below which resuming is not
// worth offering. A run that died in its first couple of turns has nothing to
// preserve, and a "继续" button on it would be noise — restarting is simpler and
// just as cheap.
const resumeWorthwhileSteps = 4

// settleJournal decides whether a finished run keeps its transcript, and marks
// the run resumable when it does. Only failures qualify: a completed run has
// nothing to resume, and a paused one is already handled by eino's own
// checkpoint (the confirm/clarify path).
func (s *ChatService) settleJournal(runID string, j *runJournal, status string) {
	if j == nil {
		return
	}
	steps := j.steps()
	if status != db.RunFailed || steps < resumeWorthwhileSteps {
		j.discard()
		return
	}
	j.flush()
	if s.Msg == nil || s.Msg.Store == nil || runID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Msg.Store.SetRunResumable(ctx, runID, steps)
}

// ResumeRun continues a run that died mid-flight, replaying its transcript into
// a fresh agent instead of starting the work over. Streams through raw exactly
// like an ordinary run, so the caller (and the UI) treat it as one.
//
// The resumed execution gets its OWN run record: the original stays in the log
// as the failure it was, and the audit trail shows a recovery rather than
// rewriting history.
func (s *ChatService) ResumeRun(ctx context.Context, runID, email string, raw func(messages.Message)) (string, error) {
	if s.Msg == nil || s.Msg.Store == nil {
		return "", errors.New("run storage unavailable")
	}
	rec, err := s.Msg.Store.GetRun(ctx, runID)
	if err != nil {
		return "", errors.New("run not found")
	}
	if rec.OwnerEmail != email {
		return "", errors.New("run not found") // don't confirm another user's run exists
	}
	if !rec.Resumable {
		return "", errors.New("这个运行无法继续(没有可恢复的记录)")
	}
	f := loadJournal(s.Cfg.Storage.BaseStoragePath, runID)
	msgs := resumeMessages(f)
	if len(msgs) == 0 {
		// The flag outlived the transcript (manually cleaned, or storage moved).
		// Correct the record rather than leaving a button that cannot work.
		_ = s.Msg.Store.ClearRunResumable(ctx, runID)
		return "", errors.New("这个运行的记录已不存在,无法继续")
	}
	reason := "failed"
	if rec.Error == "cancelled" {
		reason = "cancelled"
	} else if rec.Status == db.RunInterrupted {
		reason = "interrupted"
	}
	msgs = append(msgs, resumeNotice(reason, len(f.Messages)))

	// Consume the flag up front: a resume that itself fails will journal its own
	// transcript and set its own flag, so leaving the old one set would offer two
	// buttons resuming from the same stale point.
	_ = s.Msg.Store.ClearRunResumable(ctx, runID)

	status := s.Run(ctx, ChatRunRequest{
		Message:        rec.Prompt,
		ConversationID: rec.ConversationID,
		TaskID:         rec.TaskID,
		UserEmail:      email,
		Trigger:        "resume",
		resumeFrom:     &runResume{Messages: msgs, Reason: reason, Steps: len(f.Messages)},
	}, raw)
	dropJournal(s.Cfg.Storage.BaseStoragePath, runID)
	return status, nil
}

// ---- context carriers ----

// runResume carries a recovered transcript into StreamEinoRun. Distinct from
// the confirm/clarify resume (eino_checkpoint.go), which resumes eino's own
// checkpoint at a specific interrupt point: this one rebuilds the conversation
// for a fresh agent, which is what a crash leaves you able to do.
type runResume struct {
	Messages []*schema.Message
	Reason   string
	Steps    int
}

type runResumeKey struct{}

func withRunResume(ctx context.Context, r *runResume) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, runResumeKey{}, r)
}

func runResumeFrom(ctx context.Context) *runResume {
	r, _ := ctx.Value(runResumeKey{}).(*runResume)
	return r
}

type journalKey struct{}

func withJournal(ctx context.Context, j *runJournal) context.Context {
	if j == nil {
		return ctx
	}
	return context.WithValue(ctx, journalKey{}, j)
}

func journalFrom(ctx context.Context) *runJournal {
	j, _ := ctx.Value(journalKey{}).(*runJournal)
	return j
}
