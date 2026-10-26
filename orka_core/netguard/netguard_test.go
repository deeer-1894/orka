package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"100.64.0.1": false, "100.127.255.254": false, "169.254.169.254": false, "0.0.0.0": false,
		"::1": false, "fc00::1": false, "fe80::1": false, "::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false,
		"64:ff9b::a00:1": false, "198.18.0.1": false, "255.255.255.255": false, "224.0.0.1": false,
	} {
		if got := IsPublic(net.ParseIP(addr)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestPolicyAllowsWhatTheAdministratorListed(t *testing.T) {
	if _, err := ParseNetworks("10.0.0.0/8, nonsense"); err == nil {
		t.Fatal("a typo must be reported, not ignored")
	}
	nets, err := ParseNetworks("10.0.0.0/8, 127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Allowed: nets}
	if !p.Permits(net.ParseIP("10.9.9.9")) || !p.Permits(net.ParseIP("127.0.0.1")) || p.Permits(net.ParseIP("127.0.0.2")) || p.Permits(net.ParseIP("192.168.0.1")) {
		t.Fatal("allowlist not applied exactly")
	}
	if _, err := (Policy{}).CheckHost(context.Background(), "localhost"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("localhost = %v", err)
	}
}

func TestGuardedTransportRefusesLoopbackUnlessAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	get := func(p *Policy) error {
		c := &http.Client{Transport: p.Guard(&http.Transport{})}
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get(&Policy{}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback reached by default: %v", err)
	}
	nets, _ := ParseNetworks("127.0.0.1")
	if err := get(&Policy{Allowed: nets}); err != nil {
		t.Fatalf("an allowed network is reachable: %v", err)
	}
}
