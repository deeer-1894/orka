package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

func TestRunAccountingLegacyEvents(t *testing.T) {
	delegate := newDelegateBudget(12, 80_000)
	delegate.AddUsage(500_000, 500_000) // A delegate is not the run meter.
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"no context", nil},
		{"no budget", context.Background()},
		{"unmetered budget", withBudget(context.Background(), delegate)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &agent.RunContext{Ctx: tc.ctx, Vars: map[string]any{}}
			middlewares.AddRunTokens(rc, 651)
			middlewares.AddRunTools(rc, 2)
			got := (&ChatService{}).runAccounting(rc, ChatRunRequest{SelectedVersion: "explicit"})
			want := runAccounting{tokens: 651, toolCalls: 2, model: "explicit"}
			if got != want {
				t.Fatalf("accounting = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRunAccountingRouterOverridesRequest(t *testing.T) {
	for _, tc := range []struct {
		name        string
		strongFirst bool
		cycles      int
		model       string
		escalated   bool
	}{
		{"fast", false, 0, "fast", false},
		{"strong first", true, 0, "strong", false},
		{"escalated", false, autoEscalateAfter, "strong", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := newTestRouter(tc.strongFirst)
			step(t, router, tc.cycles)
			rc := &agent.RunContext{Vars: map[string]any{varModelRouter: router}}
			got := (&ChatService{}).runAccounting(rc, ChatRunRequest{SelectedVersion: ModelAuto})
			if got.model != tc.model || got.escalated != tc.escalated {
				t.Fatalf("accounting model = %q/%v, want %q/%v", got.model, got.escalated, tc.model, tc.escalated)
			}
		})
	}
}

// Baseline: 39 visible main-model calls were recorded, but four mini-model
// summaries were billed only to the shared budget (733068 + 75604 = 808672).
func TestRunAccountingIncludesSummarySpend(t *testing.T) {
	b := newRunBudget(200, 800_000, 0)
	rc := &agent.RunContext{Ctx: withBudget(context.Background(), b), Vars: map[string]any{}}
	for i := 0; i < 39; i++ {
		tokens := 18_000
		if i == 38 {
			tokens = 49_068
		}
		b.AddUsage(tokens, 0)
		middlewares.AddRunTokens(rc, tokens)
	}
	for i := 0; i < 4; i++ {
		b.AddUsage(18_000, 901)
	}
	middlewares.AddRunTools(rc, 109)
	b.observe(nil)
	if b.exhausted() != "tokens" {
		t.Fatal("baseline must exhaust the token budget")
	}
	got := (&ChatService{}).runAccounting(rc, ChatRunRequest{})
	if got.tokens != 808_672 {
		t.Fatalf("recorded tokens = %d, want 808672 including summaries", got.tokens)
	}
	if got.toolCalls != 109 {
		t.Fatalf("tool calls = %d, want 109", got.toolCalls)
	}
}

func TestRunAccountingMeteringAvailability(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		reported                         bool
		prompt, completion, events, want int
	}{
		{name: "no usage report falls back", events: 651, want: 651},
		{name: "reported zero is authoritative", reported: true, events: 651, want: 0},
		{name: "reported zero without events", reported: true, want: 0},
		{name: "resumed run excludes replayed usage", reported: true, prompt: 40, completion: 2, events: 500, want: 42},
		{name: "spend without final event", reported: true, prompt: 40, completion: 2, want: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newRunBudget(0, 0, 0)
			if tc.reported {
				b.AddUsage(tc.prompt, tc.completion)
			}
			rc := &agent.RunContext{Ctx: withBudget(context.Background(), b), Vars: map[string]any{}}
			middlewares.AddRunTokens(rc, tc.events)
			got := (&ChatService{}).runAccounting(rc, ChatRunRequest{})
			if got.tokens != tc.want {
				t.Fatalf("recorded tokens = %d, want %d", got.tokens, tc.want)
			}
		})
	}
}

func TestRunAccountingDefaultModel(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Model = "configured-main"
	svc := &ChatService{Cfg: cfg}
	rc := &agent.RunContext{}
	got := svc.runAccounting(rc, ChatRunRequest{})
	if got.model != cfg.LLM.Model || got.escalated {
		t.Fatalf("model = %q/%v, want configured-main/false", got.model, got.escalated)
	}
	// An explicit selection keeps its identity even when the default differs.
	got = svc.runAccounting(rc, ChatRunRequest{SelectedVersion: "explicit"})
	if got.model != "explicit" {
		t.Fatalf("explicit model = %q", got.model)
	}
}

// Observe the actual call context without replacing the production metering
// wrapper, so this checks the Run -> fast path -> usage sink wiring.
type accountingContextClient struct {
	llm.Client
	ctx      context.Context
	contexts []context.Context
}

func (c *accountingContextClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	c.ctx = ctx
	c.contexts = append(c.contexts, ctx)
	return c.Client.Chat(ctx, req)
}

func TestRunAccountingFastPathUsesSharedBudget(t *testing.T) {
	for _, decline := range []bool{false, true} {
		name := "direct answer"
		if decline {
			name = "declined probe then agent"
		}
		t.Run(name, func(t *testing.T) {
			var responses []llm.Response
			want := 42
			if decline {
				responses = append(responses, llm.Response{Content: needToolsMarker, FinishReason: "stop",
					Usage: llm.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10}})
				want += 10
			}
			responses = append(responses, llm.Response{Content: "42", FinishReason: "stop",
				Usage: llm.Usage{PromptTokens: 40, CompletionTokens: 2, TotalTokens: 42}})
			mock := llm.NewMock(responses...)
			client := &accountingContextClient{Client: mock}
			svc, _ := testService(t, llm.NewMetered(client, nil))
			svc.DisableFastPath = false
			type parentKey struct{}
			parent := context.WithValue(context.Background(), parentKey{}, "parent")
			status := svc.Run(parent, ChatRunRequest{Message: "1+1"}, func(messages.Message) {})
			if status != db.RunDone {
				t.Fatalf("status = %q, want done", status)
			}
			if mock.Calls() != len(responses) {
				t.Fatalf("calls = %d, want %d", mock.Calls(), len(responses))
			}
			if client.ctx == nil {
				t.Fatal("fast path made no model call")
			}
			if client.ctx.Value(parentKey{}) != "parent" {
				t.Error("call context lost parent values")
			}
			if client.ctx.Err() != context.Canceled {
				t.Error("call context must inherit the run's cancellation")
			}
			b := budgetFrom(client.ctx)
			if b == nil {
				t.Fatal("fast path model call has no shared run budget")
			}
			if got := b.spentTokens(); got != want {
				t.Fatalf("fast path spend = %d, want %d", got, want)
			}
			got := svc.runAccounting(&agent.RunContext{Ctx: client.ctx}, ChatRunRequest{})
			if got.tokens != want {
				t.Fatalf("fast path accounting = %d, want %d", got.tokens, want)
			}
		})
	}
}

// Exercise attachment loading and the actual metering wrapper, so prepass
// usage must reach the same final accounting as the downstream answer.
func TestRunAccountingAttachmentPrepassUsesSharedBudget(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{Content: "The image contains 42.", FinishReason: "stop", Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}},
		llm.Response{Content: "42", FinishReason: "stop", Usage: llm.Usage{PromptTokens: 40, CompletionTokens: 2, TotalTokens: 42}},
	)
	client := &accountingContextClient{Client: mock}
	svc, _ := testService(t, llm.NewMetered(client, nil))
	svc.Cfg.LLM.VLMModel = "fixture-vision"
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	root := filepath.Join(svc.Cfg.Storage.BaseStoragePath, "attachment-test")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aA1sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.png"), png, 0600); err != nil {
		t.Fatal(err)
	}
	type parentKey struct{}
	parent := context.WithValue(context.Background(), parentKey{}, "parent")
	req := ChatRunRequest{Message: "Read the attached image", UserEmail: "attachment-test", FileIDs: []string{"fixture.png"}}
	if status := svc.Run(parent, req, func(messages.Message) {}); status != db.RunDone {
		t.Fatalf("status = %q, want done", status)
	}
	if mock.Calls() != 2 {
		t.Fatalf("calls = %d, want prepass and answer", mock.Calls())
	}
	prepass := mock.Requests[0]
	if prepass.Model != "fixture-vision" || len(prepass.Messages) != 1 || len(prepass.Messages[0].Images) != 1 {
		t.Fatalf("missing image prepass: %+v", prepass)
	}
	if prepass.Messages[0].Images[0] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
		t.Fatal("prepass did not receive the attached image")
	}
	injected := false
	for _, msg := range mock.Requests[1].Messages {
		injected = injected || strings.Contains(msg.Content, "The image contains 42.")
	}
	if !injected {
		t.Fatal("answer did not receive the prepass description")
	}
	got := svc.runAccounting(&agent.RunContext{Ctx: client.ctx}, req)
	if got.tokens != 162 {
		t.Fatalf("attachment run recorded %d tokens, want 162 (120 prepass + 42 answer)", got.tokens)
	}
	for i, ctx := range client.contexts {
		if ctx.Value(parentKey{}) != "parent" || ctx.Err() != context.Canceled {
			t.Errorf("call %d lost run context ancestry/cancellation", i)
		}
		if budgetFrom(ctx) != budgetFrom(client.ctx) {
			t.Errorf("call %d did not share the answer's run budget", i)
		}
	}
}
