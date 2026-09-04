package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func bigCode(n int) string {
	return strings.Repeat("def f():\n    return compute(x, y)\n", n)
}

// file_write's payload is on disk the moment the call returns, so the history
// should point at the path rather than archive a second copy. Measured: 13
// file_write calls carried 46,774 chars of argument against 630 of result, and
// the clear pass could not see any of it.
func TestFileWriteArgumentPointsAtTheFileItWrote(t *testing.T) {
	code := bigCode(300)
	in := &schema.ToolArgument{Text: `{"path":"search-lab/corpus.py","content":` + mustJSON(code) + `}`}

	got, carried := clearedToolArgument("file_write", in, ".orka_offload/run_x/file_write-clear-call_1.txt")

	if carried != "" {
		t.Error("content is already at its path; nothing needs archiving")
	}
	if len(got.Text) >= len(in.Text)/4 {
		t.Errorf("argument barely shrank: %d -> %d chars", len(in.Text), len(got.Text))
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(got.Text), &obj); err != nil {
		t.Fatalf("replacement is not valid JSON, the model would choke: %v", err)
	}
	if obj["path"] != "search-lab/corpus.py" {
		t.Errorf("path field lost; the call no longer reads as a request: %v", obj["path"])
	}
	body, _ := obj["content"].(string)
	if !strings.Contains(body, "search-lab/corpus.py") || !strings.Contains(body, readFileToolName) {
		t.Errorf("placeholder does not say how to get the content back: %q", body)
	}
	if strings.Contains(got.Text, "return compute") {
		t.Error("payload still in context")
	}
}

// Anything else large has no other copy, so it rides along in the same offload
// file as the result — ClearResult offers exactly one file per call.
func TestOtherLargeArgumentsAreCarriedIntoTheOffloadFile(t *testing.T) {
	code := bigCode(200)
	in := &schema.ToolArgument{Text: `{"code":` + mustJSON(code) + `}`}
	path := ".orka_offload/run_x/python-clear-call_2.txt"

	got, carried := clearedToolArgument("python", in, path)

	if carried != in.Text {
		t.Error("the only copy of the argument was dropped instead of archived")
	}
	if !strings.Contains(got.Text, path) {
		t.Errorf("placeholder does not name the archive: %q", got.Text)
	}
	if len([]rune(got.Text)) > argDigestChars+200 {
		t.Errorf("placeholder is %d runes; it should be a pointer, not a copy", len([]rune(got.Text)))
	}
}

// A small argument IS the request. Losing it costs the run more than the tokens
// are worth.
func TestSmallArgumentsAreLeftAlone(t *testing.T) {
	for _, text := range []string{
		`{"url":"https://www.cloudwego.io/docs/eino/"}`,
		`{"path":"notes.md"}`,
		`{"query":"bm25 parameter tuning"}`,
		`{"path":"a.py","content":"print(1)"}`,
	} {
		in := &schema.ToolArgument{Text: text}
		got, carried := clearedToolArgument("file_write", in, "p")
		if got.Text != text || carried != "" {
			t.Errorf("small argument %s was rewritten to %q", text, got.Text)
		}
	}
}

// A model that sends something unexpected keeps its argument verbatim rather
// than having it mangled into a wrong pointer.
func TestUnexpectedArgumentShapesFallBackSafely(t *testing.T) {
	long := strings.Repeat("x", argClearAboveChars+50)
	for _, tc := range []struct{ name, text string }{
		{"not json", long},
		{"no path field", `{"content":"` + long + `"}`},
		{"path not a string", `{"path":42,"content":"` + long + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, carried := clearedToolArgument("file_write", &schema.ToolArgument{Text: tc.text}, "arch.txt")
			// Falls through to the generic path: pointer + the text preserved.
			if carried != tc.text {
				t.Errorf("argument content was lost: carried %d of %d chars", len(carried), len(tc.text))
			}
			if strings.Contains(got.Text, "已写入") {
				t.Error("claimed the payload is at a path it could not actually read")
			}
		})
	}
}

// The gate eino uses to decide whether clearing is worth doing has to sit below
// one maximal clearable item, or items accumulate under it forever.
func TestClearFloorIsTrippableByASingleMaximalResult(t *testing.T) {
	// ~3.5 chars per token on this traffic (mixed CJK/ASCII).
	maxSingleResultTokens := maxToolOutputChars * 10 / 35
	if clearFloorTokens >= maxSingleResultTokens {
		t.Errorf("floor %d >= largest single result %d: no lone result can ever trip it",
			clearFloorTokens, maxSingleResultTokens)
	}
	if clearFloorTokens <= 0 {
		t.Error("a zero floor clears on every cycle, and every clear breaks the prefix cache")
	}
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
