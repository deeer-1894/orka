package api

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := &rateLimiter{hits: map[string]*rlWindow{}, limit: 3, window: time.Minute}
	for i := 0; i < 3; i++ {
		if !rl.allow("k") {
			t.Fatalf("hit %d should be allowed", i)
		}
	}
	if rl.allow("k") {
		t.Error("4th hit should be blocked")
	}
	if !rl.allow("other") {
		t.Error("different key should be independent")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := &rateLimiter{hits: map[string]*rlWindow{}, limit: 1, window: 10 * time.Millisecond}
	if !rl.allow("k") {
		t.Fatal("first hit allowed")
	}
	if rl.allow("k") {
		t.Fatal("second hit blocked")
	}
	time.Sleep(15 * time.Millisecond)
	if !rl.allow("k") {
		t.Error("after window reset should allow again")
	}
}
