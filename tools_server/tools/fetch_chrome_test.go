package tools

import "testing"

// A documentation page's sidebar and footer are not its content. Left in, they
// were the first thing the model saw and the thing the context layer's
// placeholder ended up describing — see reChrome for the measured page.
func TestChromeStrippedButContentKept(t *testing.T) {
	html := `<html><head><title>Summarization | CloudWeGo</title></head><body>
<header><a href="/">Documentation</a>Kitex Hertz Volo Eino</header>
<nav><ul><li>Chapter 1: ChatModel and Message</li><li>Chapter 2: Runner</li></ul></nav>
<main><p>Summarization 中间件按 token 阈值自动触发历史压缩。</p></main>
<aside>About Blog Cooperation</aside>
<footer>Copyright ByteDance</footer>
</body></html>`

	got := clean(reChrome.ReplaceAllString(
		reStyle.ReplaceAllString(reScript.ReplaceAllString(html, " "), " "), " "))

	for _, junk := range []string{"Kitex", "Chapter 1", "Cooperation", "Copyright"} {
		if contains(got, junk) {
			t.Errorf("chrome %q survived:\n%s", junk, got)
		}
	}
	if !contains(got, "token 阈值自动触发历史压缩") {
		t.Errorf("stripping took the page content with it:\n%s", got)
	}
}

// The title is parsed from the raw html before chrome is removed, so a <header>
// inside <head> cannot take it away.
func TestTitleSurvivesChromeStripping(t *testing.T) {
	html := `<html><head><title>Reduction | CloudWeGo</title></head><body><nav>x</nav><p>body text here</p></body></html>`
	m := reTitle.FindStringSubmatch(html)
	if len(m) < 2 || clean(m[1]) != "Reduction | CloudWeGo" {
		t.Fatalf("title not recovered: %#v", m)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
