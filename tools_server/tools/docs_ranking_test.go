package tools

import "testing"

func TestDiscoverQueryRelevanceBeforeLanguage(t *testing.T) {
	for _, tc := range []struct{ name, index, body, query, want string }{
		{"specific translated term", "/llms.txt", "[Overview](/en/overview)\n[jitter-setting 配置](/zh/config)", "jitter-setting", "/zh/config"},
		{"URL matches specific term", "/llms.txt", "[Overview](/en/overview)\n[配置](/zh/jitter-setting)", "jitter-setting", "/zh/jitter-setting"},
		{"input language does not hide match", "/zh/catalog.md", "[简介](/zh/overview)\n[jitter-setting](/en/config)", "jitter-setting", "/en/config"},
		{"language breaks relevance tie", "/llms.txt", "[jitter-setting](/ar/config)\n[jitter-setting](/en/config)", "jitter-setting", "/en/config"},
		{"empty query retains language preference", "/llms.txt", "[Overview](/en/overview)\n[jitter-setting](/zh/config)", "", "/en/overview"},
		{"URL breaks remaining tie", "/llms.txt", "[jitter-setting](/en/z)\n[jitter-setting](/en/a)", "jitter-setting", "/en/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := docsFixture(t, map[string]string{tc.index: tc.body})
			got := discoveryResult(t, s.URL+tc.index, tc.query, 1)
			if len(got.Results) != 1 || got.Results[0].URL != s.URL+tc.want {
				t.Fatalf("wrong top result for specific query %q: %+v", tc.query, got)
			}
		})
	}
}

func TestDiscoverPagesStillOutrankMatchingIndexes(t *testing.T) {
	s := docsFixture(t, map[string]string{"/llms.txt": "[Overview](/en/overview)\n[jitter-setting catalog](/_llms/en.md)"})
	got := discoveryResult(t, s.URL, "jitter-setting", 2)
	if len(got.Results) != 2 || got.Results[0].Kind != "page" || got.Results[1].Kind != "index" {
		t.Fatalf("index displaced page: %+v", got)
	}
}
