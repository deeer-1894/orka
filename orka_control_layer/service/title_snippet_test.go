package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitleSnippetIsDeterministicAndUnicodeSafe(t *testing.T) {
	if got := titleSnippet("第一行\n第二行"); got != "第一行 第二行" {
		t.Fatalf("multiline title = %q", got)
	}
	if got := titleSnippet("  "); got != "New chat" {
		t.Fatalf("blank title = %q", got)
	}
	got := titleSnippet(strings.Repeat("测", 30))
	if got != strings.Repeat("测", 24)+"…" || !utf8.ValidString(got) {
		t.Fatalf("bounded title = %q", got)
	}
}
