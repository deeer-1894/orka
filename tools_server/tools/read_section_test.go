package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadSectionBeyondFetchCap(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": `<html><title>Guide</title><nav>Retry menu</nav><main><h1>Overview</h1><p>` + strings.Repeat("unrelated introduction. ", 2000) + `</p><h2>Retry policy</h2><p>Use exponential backoff with jitter.</p><pre>retry_count = 3</pre><h2>Billing</h2><p>BILLING_UNRELATED</p></main><script>retry SECRET_SCRIPT</script></html>`})
	res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": "retry policy", "max_chars": 500})
	if res.IsError || !strings.Contains(out, "exponential backoff") || !strings.Contains(out, "retry_count = 3") {
		t.Fatalf("missed late section: %q", out)
	}
	for _, junk := range []string{"unrelated introduction", "BILLING_UNRELATED", "SECRET_SCRIPT", "<p>", "Retry menu"} {
		if strings.Contains(out, junk) {
			t.Errorf("irrelevant content leaked: %q", junk)
		}
	}
}

func TestReadSectionMarkdownAndUnicodeBound(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide.md": "# Intro\nUnrelated.\n## 重试策略\n" + strings.Repeat("中文🙂重试内容。", 100)})
	for _, max := range []int{1, 17, 100} {
		res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide.md", "query": "重试", "max_chars": max})
		if res.IsError || !utf8.ValidString(out) || utf8.RuneCountInString(sectionBody(t, out)) > max || !strings.Contains(out, "…") {
			t.Errorf("bad bound %d: %q", max, out)
		}
	}
}

func TestReadSectionFindsMatchInsideLongSection(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": "<h1>Configuration</h1><p>" + strings.Repeat("intro ", 6000) + "needle-setting enables retries. " + strings.Repeat("tail ", 1000) + "</p>"})
	res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": "needle-setting", "max_chars": 160})
	if res.IsError || !strings.Contains(out, "needle-setting enables retries") || utf8.RuneCountInString(sectionBody(t, out)) > 160 {
		t.Fatalf("lost matching text: %q", out)
	}
}

func TestReadSectionMissingQueryOrMatch(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": "<h1>Intro</h1><p>Welcome</p>"})
	for _, query := range []string{"", "nonexistent"} {
		res, out := callDocsTool(t, "read_section", map[string]any{"url": s.URL + "/guide", "query": query})
		if !res.IsError || out == "" {
			t.Fatalf("missing explicit failure: %q", out)
		}
	}
}

func TestFetchURLUnicodeCompatibility(t *testing.T) {
	s := docsFixture(t, map[string]string{"/guide": "<title>中文</title><p>" + strings.Repeat("中", 10000) + "</p>"})
	res, out := callDocsTool(t, "fetch_url", map[string]any{"url": s.URL + "/guide"})
	if res.IsError || !utf8.ValidString(out) || !strings.Contains(out, "Title: 中文") || !strings.Contains(out, "…") {
		t.Fatalf("broken fetch compatibility: %.100s", out)
	}
}
