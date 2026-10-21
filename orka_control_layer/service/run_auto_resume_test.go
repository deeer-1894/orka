package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

func TestTransientProviderFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"rate limit exhausted ADK retries", fmt.Errorf("[NodeRunError] %w", &adk.RetryExhaustedError{LastErr: &llm.APIError{Status: 429, Body: "slow down"}, TotalRetries: 2}), true},
		{"overloaded 529", &llm.APIError{Status: 529, Body: "overloaded"}, true},
		{"bad gateway", fmt.Errorf("model: %w", &llm.APIError{Status: 502}), true},
		{"stringified 429", errors.New("[NodeRunError] exceeds max retries: last error: llm status 429: too many requests"), true},
		{"overload text", errors.New("stream error: server overload, please retry later"), true},
		{"bad request", &llm.APIError{Status: 400, Body: "invalid tool schema"}, false},
		{"auth", fmt.Errorf("[NodeRunError] %w", &adk.RetryExhaustedError{LastErr: &llm.APIError{Status: 401}}), false},
		{"cancelled", context.Canceled, false},
		{"arbitrary failure", errors.New("invalid runtime checkpoint"), false},
		{"nil", nil, false},
	} {
		if got := transientProviderFailure(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestAutoResumeNeverForKilledOrExhaustedRuns(t *testing.T) {
	s := &ChatService{Msg: &message_utils.Messenger{Store: &db.Storage{}}}
	rateLimited := &llm.APIError{Status: 429}
	req := ChatRunRequest{UserEmail: "owner"}
	if !s.autoResumeEligible(context.Background(), req, rateLimited) {
		t.Fatal("a rate-limited run should be continued")
	}
	killed, cancel := context.WithCancel(context.Background())
	cancel()
	if s.autoResumeEligible(killed, req, rateLimited) {
		t.Fatal("a run the user killed must not resume on its own")
	}
	req.autoResumes = len(autoResumeDelays)
	if s.autoResumeEligible(context.Background(), req, rateLimited) {
		t.Fatal("the backoff schedule caps automatic resumes")
	}
	if s.autoResumeEligible(context.Background(), ChatRunRequest{UserEmail: "owner", ResumeKey: "approval"}, rateLimited) {
		t.Fatal("an approval continuation has its own checkpoint and is not auto-resumed")
	}
}

// flakyProvider does one unit of real work, then is rate limited for a while,
// then recovers — the shape of a provider outage hitting a long run.
type flakyProvider struct {
	mu       sync.Mutex
	limited  int // 429s to return after the first call
	calls    int
	requests []llm.Request
}

func (p *flakyProvider) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	switch {
	case p.calls == 1:
		return llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "step-1", Name: "record_step", Arguments: `{"step":"collect"}`}}}, nil
	case p.calls <= 1+p.limited:
		return llm.Response{}, &llm.APIError{Status: 429, Body: "System protection triggered by request burst"}
	default:
		return llm.Response{Content: "全部完成", FinishReason: "stop"}, nil
	}
}

type autoResumeFixture struct {
	store    *db.Storage
	cfg      *config.Config
	provider *flakyProvider
	mu       sync.Mutex
	executed int
}

func newAutoResumeFixture(t *testing.T, ctx context.Context, limited int) *autoResumeFixture {
	t.Helper()
	uri := os.Getenv("ORKA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ORKA_TEST_MONGO_URI for isolated Mongo integration")
	}
	store, err := db.NewStorage(ctx, uri, "orka_auto_resume_test_"+messages.NewID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Runs.Database().Drop(context.Background()); store.Close(context.Background()) })
	if err := store.CreateConversation(ctx, &db.ConversationTable{ConversationID: "conv", OwnerEmail: "owner", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	saved := autoResumeDelays
	autoResumeDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { autoResumeDelays = saved })
	cfg := &config.Config{}
	cfg.LLM.Model, cfg.LLM.Models = "m", []string{"m"}
	cfg.Agent.CheckpointTTLSec = 3600
	cfg.Storage.BaseStoragePath = t.TempDir()
	return &autoResumeFixture{store: store, cfg: cfg, provider: &flakyProvider{limited: limited}}
}

// process builds a control plane from nothing but durable state, which is all
// a restart between the failure and the retry leaves behind.
func (f *autoResumeFixture) process() *ChatService {
	svc := NewChatService(f.cfg, llm.NewAccounted(f.provider), checkpoint.NewMemoryStore(), message_utils.New(f.store, 1.0, nil), obs.NewMetrics(), nil)
	svc.UsageLedger = &fakeLedger{}
	svc.DisableSummary, svc.DisableFastPath = true, true
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{retrievalFixture{"record_step", func(context.Context, map[string]any) (string, error) {
			f.mu.Lock()
			f.executed++
			f.mu.Unlock()
			return "step recorded: collect", nil
		}}}, nil, nil
	}
	return svc
}

func (f *autoResumeFixture) runs(t *testing.T, ctx context.Context) []db.RunRecord {
	t.Helper()
	runs, err := f.store.ListRuns(ctx, bson.M{"conversation_id": "conv"}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	return runs // newest first
}

func TestTransientFailureResumesAutomaticallyAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The ADK layer retries each call twice, so three 429s exhaust one step.
	f := newAutoResumeFixture(t, ctx, modelMaxRetries+1)

	report := &runReport{}
	status := f.process().Run(ctx, ChatRunRequest{Message: "collect then summarise", ConversationID: "conv", UserEmail: "owner", Trigger: "rerun", report: report}, func(messages.Message) {})
	if status != db.RunFailed || !report.AutoResumeScheduled {
		t.Fatalf("status=%s scheduled=%v, want a failed run with a scheduled resume", status, report.AutoResumeScheduled)
	}
	runs := f.runs(t, ctx)
	if len(runs) != 1 {
		t.Fatalf("runs=%+v", runs)
	}
	failed := runs[0]
	if !failed.Resumable || failed.AutoResumeAt == 0 || failed.AutoResumeAttempt != 1 || !strings.Contains(failed.Error, "429") || len(failed.Unfinished) == 0 {
		t.Fatalf("failed run not scheduled for an automatic resume: %+v", failed)
	}
	if n, _ := f.store.Notifications.CountDocuments(ctx, bson.M{"owner_email": "owner"}); n != 0 {
		t.Fatalf("a failure that will be retried notified the user %d times", n)
	}

	// "Restart": a fresh service with no in-memory state finds the due record.
	time.Sleep(5 * time.Millisecond)
	f.process().sweepAutoResumes(ctx).Wait()

	runs = f.runs(t, ctx)
	if len(runs) != 2 {
		t.Fatalf("runs=%+v", runs)
	}
	resumed, original := runs[0], runs[1]
	if resumed.Status != db.RunDone || resumed.ResumedFrom != failed.RunID || resumed.Trigger != "resume" {
		t.Fatalf("continuation=%+v, want a completed resume of %s", resumed, failed.RunID)
	}
	if original.Resumable || original.AutoResumeAt != 0 {
		t.Fatalf("original still offers a resume: %+v", original)
	}
	if loadJournal(f.cfg.Storage.BaseStoragePath, failed.RunID) != nil {
		t.Fatal("a completed continuation should consume its predecessor's journal")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.executed != 1 {
		t.Fatalf("record_step ran %d times; the resume must not repeat completed tool calls", f.executed)
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if f.provider.calls != 1+f.provider.limited+1 {
		t.Fatalf("provider calls=%d", f.provider.calls)
	}
	last := f.provider.requests[len(f.provider.requests)-1]
	replayed := false
	for _, m := range last.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "step recorded") {
			replayed = true
		}
	}
	if !replayed {
		t.Fatal("resumed model call did not see the completed tool result")
	}
}

// A continuation that hits the same outage schedules the next one, carrying
// the attempt count, until the schedule runs out.
func TestAutoResumeChainIsCapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := newAutoResumeFixture(t, ctx, 1<<20) // never recovers
	f.process().Run(ctx, ChatRunRequest{Message: "collect then summarise", ConversationID: "conv", UserEmail: "owner", Trigger: "rerun"}, func(messages.Message) {})
	for attempt := 1; attempt <= len(autoResumeDelays); attempt++ {
		latest := f.runs(t, ctx)[0]
		if latest.Status != db.RunFailed || latest.AutoResumeAttempt != attempt || latest.AutoResumeAt == 0 {
			t.Fatalf("attempt %d: latest=%+v", attempt, latest)
		}
		time.Sleep(5 * time.Millisecond)
		f.process().sweepAutoResumes(ctx).Wait()
	}
	runs := f.runs(t, ctx)
	if len(runs) != 1+len(autoResumeDelays) {
		t.Fatalf("%d runs, want the original plus %d automatic resumes", len(runs), len(autoResumeDelays))
	}
	if last := runs[0]; last.AutoResumeAt != 0 || !last.Resumable {
		t.Fatalf("the last failure should wait for the user, still resumable by hand: %+v", last)
	}
	if n, _ := f.store.Notifications.CountDocuments(ctx, bson.M{"owner_email": "owner"}); n != 1 {
		t.Fatalf("want exactly one failure notification once retries ran out, got %d", n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.executed != 1 {
		t.Fatalf("record_step ran %d times across resumes", f.executed)
	}
}

func TestNewTurnSupersedesPendingAutoResume(t *testing.T) {
	uri := os.Getenv("ORKA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ORKA_TEST_MONGO_URI for isolated Mongo integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := db.NewStorage(ctx, uri, "orka_auto_resume_test_"+messages.NewID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Runs.Database().Drop(context.Background()); store.Close(context.Background()) }()
	if err := store.CreateRun(ctx, &db.RunRecord{RunID: "old", ConversationID: "conv", OwnerEmail: "owner", Status: db.RunFailed}); err != nil {
		t.Fatal(err)
	}
	_ = store.SetRunResumable(ctx, "old", 6)
	if ok, err := store.ScheduleRunAutoResume(ctx, "old", time.Now().Add(time.Hour).UnixMilli(), 1); err != nil || !ok {
		t.Fatalf("schedule ok=%v err=%v", ok, err)
	}
	s := &ChatService{Msg: &message_utils.Messenger{Store: store}}
	if id := s.createRun(ctx, ChatRunRequest{ConversationID: "conv", UserEmail: "owner", Message: "something else"}, messages.Meta{}, "manual"); id == "" {
		t.Fatal("run not created")
	}
	rec, err := store.GetRun(ctx, "old")
	if err != nil || rec.AutoResumeAt != 0 || !rec.Resumable {
		t.Fatalf("pending resume survived a new turn or lost its manual offer: %+v err=%v", rec, err)
	}
	if ok, _ := store.CancelRunAutoResume(ctx, "old", "owner"); ok {
		t.Fatal("nothing should remain to cancel")
	}
}
