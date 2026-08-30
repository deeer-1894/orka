package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
)

// run_digest.go — what a finished run leaves behind for the turns after it.
//
// A conversation used to carry only the assistant's closing prose from turn to
// turn. Measured on this deployment that discards most of the work: 3,783 tool
// messages against 1,383 chat ones, so an agent that spent ten minutes
// researching arrived at the follow-up question knowing only its own summary.
//
// The digest is deliberately NOT the transcript. Replaying tool results
// verbatim would cost 76k tokens on one real conversation — more than the run
// that produced them. It is a compaction, and it is built in two halves for a
// reason grounded in what these runs actually do:
//
//   - Facts are mechanical. file_write averages 78 characters ("wrote N bytes
//     to X"); there is nothing for a model to add, and a paraphrased file path
//     is worse than no path because later turns will cite it.
//   - Learned needs a model. The dominant tools are retrieval — fetch_url
//     averages 3,024 characters, http_request 14,039 — and the value is in the
//     content. Truncating to a fixed prefix returns page furniture, not
//     findings, which would fill the context with noise.
//
// The model half runs AFTER the user has their answer (the titleAsync pattern
// in adk_chat.go), so it costs no latency, and its failure degrades the digest
// to facts-only rather than losing the turn.

const (
	// digestKeep is how many runs of history a conversation carries. Each digest
	// is a few hundred tokens, so this stays a small preamble rather than
	// becoming the context problem it exists to solve.
	digestKeep = 8
	// digestMaxFacts caps the factual spine. A run that writes forty files is
	// summarised by its first few plus a count.
	digestMaxFacts = 12
	// digestSourceChars is how much transcript the model summarises. Enough to
	// cover a long run's findings, small enough to stay a cheap mini-model call.
	digestSourceChars = 12000
	// digestLearnedChars caps the model's output so one verbose run cannot
	// dominate the preamble.
	digestLearnedChars = 700
)

// factTools produce durable, citable effects. Their results are short and
// structured, so they are recorded verbatim and never sent through a model.
var factTools = map[string]bool{
	"file_write":       true,
	"file_delete":      true,
	"artifact_publish": true,
	"ingest_factor":    true,
	"skill_create":     true,
}

// buildDigest derives a run's digest from its journal. The factual half is
// complete on return; the model half is filled in asynchronously by
// digestAsync, so a caller that skips it still gets a useful record.
func buildDigest(runID, prompt string, msgs []*schema.Message) db.RunDigest {
	d := db.RunDigest{
		RunID:  runID,
		At:     time.Now().UnixMilli(),
		Prompt: trunc(prompt, 160),
	}
	// Pair each tool result with the call that produced it: the result alone says
	// what happened, the call says what was asked for.
	names := map[string]string{}
	args := map[string]string{}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			names[tc.ID] = tc.Function.Name
			args[tc.ID] = tc.Function.Arguments
		}
	}
	extra := 0
	for _, m := range msgs {
		if m == nil || m.Role != schema.Tool {
			continue
		}
		d.Tools++
		name := names[m.ToolCallID]
		if name == "" {
			name = m.ToolName
		}
		if !factTools[name] {
			continue
		}
		if len(d.Facts) >= digestMaxFacts {
			extra++
			continue
		}
		d.Facts = append(d.Facts, describeFact(name, args[m.ToolCallID], m.Content))
	}
	if extra > 0 {
		d.Facts = append(d.Facts, "…以及另外 "+itoa(extra)+" 项同类操作")
	}
	return d
}

// describeFact renders one durable effect as a single citable line. Prefers the
// tool's own result text (already short and precise for these tools) and falls
// back to the requested path when a tool reports nothing useful.
func describeFact(name, rawArgs, result string) string {
	result = strings.TrimSpace(result)
	if result != "" && len(result) <= 200 {
		return name + ": " + result
	}
	var a map[string]any
	if rawArgs != "" && json.Unmarshal([]byte(rawArgs), &a) == nil {
		for _, k := range []string{"path", "file", "name", "title", "id"} {
			if v, ok := a[k].(string); ok && v != "" {
				return name + ": " + v
			}
		}
	}
	return name + ": " + trunc(result, 120)
}

// digestAsync writes the model half and stores the finished digest. Detached on
// purpose: the digest matters on the NEXT turn, so making the current one wait
// for it would be paying latency for nobody.
func (s *ChatService) digestAsync(convID string, d db.RunDigest, msgs []*schema.Message) {
	if s.Msg == nil || s.Msg.Store == nil || convID == "" {
		return
	}
	model, modelName := s.modelFor("mini")
	source := digestSource(msgs)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if model != nil && source != "" {
			if learned := s.summarizeFindings(ctx, model, modelName, d.Prompt, source); learned != "" {
				d.Learned = learned
			}
		}
		// Store regardless: a digest with facts and no prose still beats the
		// amnesia this replaces.
		if err := s.Msg.Store.AppendRunDigest(ctx, convID, d, digestKeep); err != nil && s.Log != nil {
			s.Log.Warn("append run digest failed", "conversation_id", convID, "err", err)
		}
	}()
}

// summarizeFindings asks the fast model what the run LEARNED. The prompt is
// narrow on purpose: this output is model-written and therefore fallible, so it
// is confined to the one thing a model is needed for. Anything a later turn
// might cite as fact comes from the deterministic half instead.
func (s *ChatService) summarizeFindings(ctx context.Context, model llm.Client, modelName, prompt, source string) string {
	resp, err := model.Chat(ctx, llm.Request{Model: modelName, Messages: []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "你在为一个 AI agent 压缩它刚完成的一轮工作,供它在下一轮回忆。\n" +
			"只写这轮**查到/得出了什么**——具体的结论、数据、事实。\n" +
			"不要复述它做了哪些操作(那部分已单独记录)。不要写开场白、不要总结体裁。\n" +
			"不确定的内容宁可省略,也不要编造:这段文字会被当作记忆使用。\n" +
			"用与用户相同的语言,300 字以内,直接给要点。"},
		{Role: llm.RoleUser, Content: "本轮任务:" + prompt + "\n\n工具返回的原始内容:\n" + source},
	}})
	if err != nil {
		return ""
	}
	return trunc(strings.TrimSpace(resp.Content), digestLearnedChars)
}

// digestSource collects the retrieval output worth summarising. Fact tools are
// excluded — they are already captured precisely — and the newest results are
// taken first, because a run's later findings build on its earlier ones.
func digestSource(msgs []*schema.Message) string {
	names := map[string]string{}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			names[tc.ID] = tc.Function.Name
		}
	}
	var parts []string
	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Role != schema.Tool || strings.TrimSpace(m.Content) == "" {
			continue
		}
		name := names[m.ToolCallID]
		if name == "" {
			name = m.ToolName
		}
		if factTools[name] || name == planToolName {
			continue
		}
		chunk := "[" + name + "] " + trunc(m.Content, 2000)
		if total+len(chunk) > digestSourceChars {
			break
		}
		parts = append(parts, chunk)
		total += len(chunk)
	}
	// Restore chronological order so the model reads the run forwards.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n\n")
}

// digestPreamble renders a conversation's digests as the memory a new turn
// starts from. Returns "" when there is nothing to recall, so a first turn is
// unaffected.
func digestPreamble(ds []db.RunDigest) string {
	if len(ds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[本次会话中你此前已完成的工作]\n")
	for _, d := range ds {
		b.WriteString("\n· 任务:" + d.Prompt + "\n")
		if len(d.Facts) > 0 {
			b.WriteString("  产出:" + strings.Join(d.Facts, ";") + "\n")
		}
		if d.Learned != "" {
			b.WriteString("  发现:" + d.Learned + "\n")
		}
	}
	b.WriteString("\n以上是记录,不是用户的新指令。产出部分可直接引用;发现部分若要作为事实使用,请先核实。")
	return b.String()
}
