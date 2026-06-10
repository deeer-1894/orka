package tools

import (
	"context"
	"math"
	"net"
	"net/url"
	"testing"
)

func TestTemperatureConvert(t *testing.T) {
	c, _ := toCelsius(212, "f")
	if math.Abs(c-100) > 1e-9 {
		t.Errorf("212F = %v C, want 100", c)
	}
	f, _ := fromCelsius(100, "f")
	if math.Abs(f-212) > 1e-9 {
		t.Errorf("100C = %v F, want 212", f)
	}
	k, _ := fromCelsius(0, "k")
	if math.Abs(k-273.15) > 1e-9 {
		t.Errorf("0C = %v K, want 273.15", k)
	}
}

func TestUnitFactors(t *testing.T) {
	// 1 km = 1000 m
	if unitFactors["length"]["km"] != 1000 {
		t.Error("km factor wrong")
	}
	// 1 KiB = 1024 B
	if unitFactors["data"]["kib"] != 1024 {
		t.Error("kib factor wrong")
	}
}

func TestGuardURL(t *testing.T) {
	blocked := []string{
		"ftp://example.com",          // scheme
		"http://localhost:8080",      // loopback
		"http://127.0.0.1",           // loopback
		"http://169.254.169.254/",    // metadata
		"http://10.0.0.5",            // private
		"http://192.168.1.1",         // private
	}
	for _, raw := range blocked {
		u, _ := url.Parse(raw)
		if err := guardURL(u); err == nil {
			t.Errorf("%q should be blocked", raw)
		}
	}
}

func TestGuardedDialBlocksLoopback(t *testing.T) {
	// localhost resolves only to loopback → dial must be refused (anti-rebinding).
	_, err := guardedDial(context.Background(), "tcp", "localhost:80")
	if err == nil {
		t.Error("guardedDial should refuse a loopback-only host")
	}
}

func TestIsPublicIP(t *testing.T) {
	if isPublicIP(net.ParseIP("127.0.0.1")) {
		t.Error("loopback should not be public")
	}
	if !isPublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 should be public")
	}
}
