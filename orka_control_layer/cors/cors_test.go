package cors

import "testing"

func TestAllowedHost(t *testing.T) {
	allowed := map[string]bool{"orka.bytedance.net": true, "localhost": true}

	cases := []struct {
		origin string
		want   bool
		note   string
	}{
		{"https://orka.bytedance.net", true, "exact host"},
		{"https://orka.bytedance.net:8443", true, "exact host with port"},
		{"http://localhost:3000", true, "localhost dev"},
		{"", false, "empty origin (non-browser)"},
		{"https://evil.com", false, "unrelated host"},
		// substring-match vulnerabilities that must NOT pass:
		{"https://orka.bytedance.net.attacker.com", false, "suffix spoof"},
		{"https://notorka.bytedance.net", false, "prefix spoof"},
		{"https://attacker.com/?x=orka.bytedance.net", false, "path contains host"},
		{"not a url", false, "unparseable -> no host"},
	}
	for _, tc := range cases {
		if got := AllowedHost(tc.origin, allowed); got != tc.want {
			t.Errorf("%s: AllowedHost(%q) = %v, want %v", tc.note, tc.origin, got, tc.want)
		}
	}
}
