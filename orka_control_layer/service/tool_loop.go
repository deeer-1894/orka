package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
)

// tool_loop.go — stop an agent re-asking a question it has already answered.
//
// Twice now a long run has done its actual work and then burned the rest of its
// budget on a loop. The clearest case: a six-framework research task produced
// all nine deliverables — six briefs, a matrix, a chart and a 5.4KB report — and
// then called file_list FIVE times with identical arguments, receiving identical
// output each time, until the budget ran out. It was filed partial with "write
// the report" still open, because it never managed to confirm the file it had
// just written.
//
// The workspace had 46 entries by then, so the listing was long and the file it
// was looking for was not in the part it could take in. Re-listing produced the
// same wall of text, so it re-listed again. Nothing in the loop could break it:
// each call succeeded, so no error surfaced, and the result was identical every
// time, so the context gained nothing.
//
// Detection is cheap — identical tool, identical arguments, same result — and
// the intervention is to say so. The call is not blocked and no error is
// returned: the model is told the result is unchanged and asked to move on,
// which is information it did not have and cannot derive, since from inside the
// loop every attempt looks like a fresh successful call.

// repeatBeforeNudge is how many identical calls are tolerated before the result
// is annotated. Two identical calls are ordinary — re-reading a file after
// writing it is good practice. The third is a loop.
const repeatBeforeNudge = 3

// loopDetector counts identical (tool, args) calls within one run.
type loopDetector struct {
	mu   sync.Mutex
	seen map[string]int
	last map[string]string // key → the result last returned, to confirm nothing changed
}

func newLoopDetector() *loopDetector {
	return &loopDetector{seen: map[string]int{}, last: map[string]string{}}
}

// observe records a completed call and reports the note to append, or "" when
// the call is not part of a loop. A call whose RESULT changed resets the count:
// polling something that is genuinely moving is legitimate work, not a loop.
func (d *loopDetector) observe(key, result string) string {
	if d == nil {
		return ""
	}
	digest := sha256.Sum256([]byte(loopObservation(key, result)))
	result = hex.EncodeToString(digest[:])
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.last[key]; ok && prev != result {
		d.seen[key] = 1
		d.last[key] = result
		return ""
	}
	d.last[key] = result
	d.seen[key]++
	if d.seen[key] < repeatBeforeNudge {
		return ""
	}
	return "\n\n[系统] 你已经用完全相同的参数调用了这个工具 " + itoa(d.seen[key]) +
		" 次,每次返回的有效结果都一样。再调一次不会得到新信息。" +
		"相同返回值不代表操作成功，也不代表验收通过。请根据返回结果修复失败或继续未完成步骤。" +
		"如果你在找某样东西却没找到,换一种方式(更具体的路径、别的工具),不要重复同一个调用。"
}

// Timing jitter is not recovery evidence. Normalize only explicit browser
// failures; preserve page identity/epoch, error details and all other results.
func loopObservation(key, result string) string {
	if !strings.HasPrefix(key, "browser\x00") {
		return result
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(result))
	decoder.UseNumber()
	if !json.Valid([]byte(result)) || decoder.Decode(&value) != nil || value["ok"] != false {
		return result
	}
	failure, ok := value["error"].(map[string]any)
	if !ok {
		return result
	}
	if code, ok := failure["code"].(string); !ok || code == "" {
		return result
	}
	delete(value, "elapsed_ms")
	normalized, err := json.Marshal(value)
	if err != nil {
		return result
	}
	return string(normalized)
}

// ---- context carrier ----

type loopKey struct{}

func withLoopDetector(ctx context.Context, d *loopDetector) context.Context {
	if d == nil {
		return ctx
	}
	return context.WithValue(ctx, loopKey{}, d)
}

func loopDetectorFrom(ctx context.Context) *loopDetector {
	d, _ := ctx.Value(loopKey{}).(*loopDetector)
	return d
}
