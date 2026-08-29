package service

import (
	"context"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
)

// eino_resilience.go — ADK-level retry + failover for model calls.
//
// Why this exists on top of the transport retry in llm/retry.go: that wrapper
// CANNOT retry a stream that already emitted a delta ("a partial stream can't be
// replayed"), so a mid-stream `unexpected EOF` surfaced as a hard failure and,
// in a long multi-step run, killed the whole pipeline. ADK's retry consumes the
// entire stream before deciding, so it can retry the model call even when the
// failure happened mid-stream — the run survives.
//
// Layering: transport retry still handles blips before the first byte (cheap,
// no duplicate tokens); ADK retry is the backstop for mid-stream failures;
// failover switches model tier when retries are exhausted.

const (
	modelMaxRetries   = 2 // model-call retries per generation step (3 calls total)
	modelFailoverTries = 1 // then try the other model tier once
)

// modelRetryConfig retries transient model failures at the ADK level. It reuses
// llm.IsTransient so the two layers agree on what is worth retrying, and emits a
// stream-reset so the UI discards the partial text from the failed attempt
// instead of concatenating it with the retry's output.
func modelRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: modelMaxRetries,
		ShouldRetry: func(ctx context.Context, rc *adk.RetryContext) *adk.RetryDecision {
			if rc == nil || rc.Err == nil {
				return nil // no error → accept the output as-is
			}
			if !llm.IsTransient(ctx, rc.Err) {
				return nil // 4xx/auth/moderation won't improve on retry
			}
			emitStreamReset(ctx) // drop the partial delta the UI already rendered
			return &adk.RetryDecision{Retry: true}
		},
		BackoffFunc: func(_ context.Context, attempt int) time.Duration {
			d := time.Duration(attempt) * 700 * time.Millisecond
			if d > 4*time.Second {
				d = 4 * time.Second
			}
			return d
		},
	}
}

// modelFailoverConfig switches to a backup model (the other tier) once retries
// are exhausted, so one provider-side bad patch doesn't end a 15-minute run.
// Returns nil when no distinct backup is available.
func modelFailoverConfig(backup model.BaseChatModel) *adk.ModelFailoverConfig[*schema.Message] {
	if backup == nil {
		return nil
	}
	return &adk.ModelFailoverConfig[*schema.Message]{
		MaxRetries: modelFailoverTries,
		ShouldFailover: func(_ context.Context, _ *schema.Message, err error) bool {
			return err != nil
		},
		GetFailoverModel: func(ctx context.Context, _ *adk.FailoverContext[*schema.Message]) (
			model.BaseModel[*schema.Message], []*schema.Message, error) {
			emitStreamReset(ctx)
			return backup, nil, nil // nil messages = reuse the original input
		},
	}
}

// backupModel returns a model on the other tier, or nil when both tiers resolve
// to the same model (failover to yourself buys nothing).
func backupModel(client llm.Client, modelName, currentModel string) model.BaseChatModel {
	if client == nil || modelName == "" || modelName == currentModel {
		return nil
	}
	return llm.NewEinoModel(client, modelName)
}

// emitStreamReset tells the UI to drop the transient streaming bubble, so a
// retried turn doesn't render the failed attempt's partial text followed by the
// new attempt's full text. Best-effort: silently a no-op in headless runs.
func emitStreamReset(ctx context.Context) {
	if emit := agent.EmitFrom(ctx); emit != nil {
		emit(messages.StreamReset(agent.MetaFrom(ctx)))
	}
}
