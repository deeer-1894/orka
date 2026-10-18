package llm

import "encoding/json"

// Broken provider counters retain valid partial counts without becoming known
// zero. Completeness is determined from field presence, never struct defaults.
type wireUsage struct{ usage Usage }

func (u *wireUsage) UnmarshalJSON(raw []byte) error {
	u.usage = Usage{Incomplete: true}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil
	}
	invalid := false
	count := func(name string) (int, bool) {
		value, present := fields[name]
		if !present {
			return 0, false
		}
		var n *int
		if json.Unmarshal(value, &n) != nil || n == nil || *n < 0 {
			invalid = true
			return 0, false
		}
		return *n, true
	}
	p, hasPrompt := count("prompt_tokens")
	c, hasCompletion := count("completion_tokens")
	total, hasTotal := count("total_tokens")
	u.usage.PromptTokens, u.usage.CompletionTokens, u.usage.TotalTokens = p, c, total
	if detail, ok := fields["completion_tokens_details"]; ok && string(detail) != "null" {
		var details map[string]json.RawMessage
		if json.Unmarshal(detail, &details) != nil || details == nil {
			invalid = true
		} else if value, ok := details["reasoning_tokens"]; ok {
			var n *int
			if json.Unmarshal(value, &n) != nil || n == nil || *n < 0 {
				invalid = true
			} else {
				u.usage.ReasoningTokens = *n
			}
		}
	}
	if p > int(^uint(0)>>1)-c {
		return nil
	}
	sum := p + c
	if hasTotal && total < sum {
		invalid = true
	}
	if !hasTotal && hasPrompt && hasCompletion {
		u.usage.TotalTokens = sum
	}
	complete := !invalid && (hasTotal || (hasPrompt && hasCompletion))
	u.usage.Known, u.usage.Incomplete = complete, !complete
	return nil
}

func (u *wireUsage) asUsage() Usage {
	if u == nil {
		return Usage{Incomplete: true}
	}
	return u.usage
}

// Preserve earlier observed lower bounds if a later usage frame is incomplete.
func mergeStreamUsage(previous, next Usage) Usage {
	if next.Known && !next.Incomplete {
		return next
	}
	next.PromptTokens = max(previous.PromptTokens, next.PromptTokens)
	next.CompletionTokens = max(previous.CompletionTokens, next.CompletionTokens)
	next.TotalTokens = max(previous.TotalTokens, next.TotalTokens)
	next.ReasoningTokens = max(previous.ReasoningTokens, next.ReasoningTokens)
	return next
}
