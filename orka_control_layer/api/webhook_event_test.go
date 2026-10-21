package api

import "testing"

func TestWebhookEventNamesGitHubAndGenericCalls(t *testing.T) {
	for _, tc := range []struct {
		header string
		body   map[string]any
		want   string
	}{
		{"pull_request", map[string]any{"action": "closed"}, "pull_request.closed"},
		{"push", map[string]any{}, "push"},
		{"", map[string]any{"event": "deploy.finished"}, "deploy.finished"},
		{"", map[string]any{}, ""},
	} {
		if got := webhookEvent(tc.header, tc.body); got != tc.want {
			t.Errorf("%q %v = %q want %q", tc.header, tc.body, got, tc.want)
		}
	}
}
