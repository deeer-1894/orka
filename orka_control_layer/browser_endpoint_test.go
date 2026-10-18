package main

import "testing"

func TestBrowserEndpointUsesConfiguredGUIOrigin(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ws://gui:8000/api/v1/ws", "ws://gui:8000/api/v1/browser/cdp/ws"},
		{"wss://gui.example.test/custom?secret=private#fragment", "wss://gui.example.test/api/v1/browser/cdp/ws"},
	} {
		got, err := browserEndpoint(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("endpoint=%s err=%v", got, err)
		}
	}
	for _, in := range []string{"", "file:///tmp/browser", "ws://user:password@host/path", "https://gui/path"} {
		if _, err := browserEndpoint(in); err == nil {
			t.Fatalf("invalid origin accepted: %s", in)
		}
	}
}
