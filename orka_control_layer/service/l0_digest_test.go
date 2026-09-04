package service

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/schema"
)

// The three placeholders below are the ones a 178-tool-call research run
// actually put in context, reproduced from the archive it left behind. Each is
// 260 chars of head-of-text and none of them says what the page CONTAINED,
// which is why the model read the files back 101 times.

const einoReductionPage = `URL: https://www.cloudwego.io/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_toolreduction/
Title: Reduction | CloudWeGo
Reduction | CloudWeGo MaxLengthForTrunc? │ │ Yes → Truncate content, save full content to Backend │ │ No → Return as-is │ └───────
DocumentationKitex
Hertz
Volo
Eino
Reduction 中间件在工具输出进入上下文前对其做两阶段处理：truncation 按长度截断单条结果，clear 按 token 阈值清理历史中的旧结果。
两个阶段都可以把完整内容写入 Backend，模型再用 read file 工具取回。`

func TestDigestKeepsWhatTheHeadTruncationThrewAway(t *testing.T) {
	got := l0Digest(einoReductionPage, offloadAbstractChars)

	// The finding — what the page is actually about.
	for _, want := range []string{"truncation", "clear", "Backend"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest lost %q, the substance of the page:\n%s", want, got)
		}
	}
	// The 120-char URL is already carried alongside in ToolArgument.
	if strings.Contains(got, "https://") {
		t.Errorf("digest spent budget echoing the URL:\n%s", got)
	}
	// Site chrome.
	for _, junk := range []string{"DocumentationKitex", "Hertz", "Volo"} {
		if strings.Contains(got, junk) {
			t.Errorf("digest kept navigation %q:\n%s", junk, got)
		}
	}
	// Box drawing is stripped, but the text between the glyphs survives.
	if strings.ContainsRune(got, '│') {
		t.Errorf("digest kept box-drawing glyphs:\n%s", got)
	}
	if !strings.Contains(got, "Truncate content") {
		t.Errorf("digest dropped diagram text worth keeping:\n%s", got)
	}
	if !strings.Contains(got, "Title: Reduction | CloudWeGo") {
		t.Errorf("digest dropped the title:\n%s", got)
	}
}

// The worst measured placeholder: 260 chars of nothing but nested archive
// headers, because the result being cleared was itself a read of an archive
// that was itself a read of an archive.
func TestDigestRejectsNestedArchiveHeaders(t *testing.T) {
	nested := `# file_read
# 请求:{"path": ".orka_offload/run_70746c4c/file_read-clear-call_3jrjll82.txt"}
# 归档于 2026-09-04T08:06:25Z

# fetch_url
# 请求:{"url": "https://www.cloudwego.io/docs/eino/"}
# 归档于 2026-09-04T08:04:12Z

Title: Reduction | CloudWeGo
Reduction 中间件对工具输出做两阶段处理。`

	got := l0Digest(nested, offloadAbstractChars)
	if strings.Contains(got, "归档于") || strings.Contains(got, "请求:") {
		t.Errorf("digest is describing its own nesting instead of the content:\n%s", got)
	}
	if !strings.Contains(got, "两阶段") {
		t.Errorf("digest never reached the content under the headers:\n%s", got)
	}
}

func TestDigestFallsBackRatherThanReturningNothing(t *testing.T) {
	// Every line fails the filters: short, non-CJK, unpunctuated.
	if got := l0Digest("Hertz\nVolo\nEino\n", offloadAbstractChars); got == "" {
		t.Error("an empty placeholder tells the model less than a bad one")
	}
}

func TestDigestHonoursItsBudget(t *testing.T) {
	long := strings.Repeat("上下文管理机制的实现细节。", 400)
	if n := len([]rune(l0Digest(long, offloadAbstractChars))); n > offloadAbstractChars {
		t.Errorf("digest ran to %d runes, budget is %d", n, offloadAbstractChars)
	}
}

// A markdown outline is the densest available description of a document, so it
// survives the navigation filter that would otherwise reject a short line.
func TestDigestKeepsMarkdownHeadings(t *testing.T) {
	got := l0Digest("# Reduction\n## Config\nSome prose that is long enough to count as content.\n", offloadAbstractChars)
	if !strings.Contains(got, "# Reduction") || !strings.Contains(got, "## Config") {
		t.Errorf("digest dropped the outline:\n%s", got)
	}
}

func detail(tool, args, result string) *reduction.ToolDetail {
	return &reduction.ToolDetail{
		ToolArgument: &schema.ToolArgument{Text: args},
		ToolResult:   &schema.ToolResult{Parts: []schema.ToolOutputPart{{Text: result}}},
	}
}

// Clearing a read of the archive into a NEW archive file is what turned one
// eviction into a four-level chain; 65 of 101 re-reads were of files whose
// content was itself a file_read result.
func TestReadBackOfAnArchiveIsNotArchivedAgain(t *testing.T) {
	src := ".orka_offload/run_70746c4c/fetch_url-clear-call_jvpq32uk.txt"
	for _, tc := range []struct{ name, tool, args string }{
		{"file_read", "file_read", `{"path": "` + src + `"}`},
		{"shell grep", "shell", `{"command": "grep -n Reduction ` + src + `"}`},
		{"python", "python", `{"code": "open('` + src + `').read()"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := archivedSource(detail(tc.tool, tc.args, "body")); got != src {
				t.Errorf("archivedSource = %q, want %q — this result would be re-archived", got, src)
			}
		})
	}
}

func TestOriginalResultsStillGetArchived(t *testing.T) {
	for _, args := range []string{
		`{"url": "https://www.cloudwego.io/docs/eino/"}`,
		`{"path": "notes.md"}`,
		`{"query": "eino reduction middleware"}`,
	} {
		if got := archivedSource(detail("fetch_url", args, "body")); got != "" {
			t.Errorf("args %s wrongly treated as an archive read-back (%q)", args, got)
		}
	}
}
