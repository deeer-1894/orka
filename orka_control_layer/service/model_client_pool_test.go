package service

import (
	"github.com/orka-oss/orka_control_layer/llm"
	"testing"
	"time"
)

func TestModelClientPoolPinsActiveAndExpiresIdleSecrets(t *testing.T) {
	p := &modelClientPool{capacity: 1, idleTTL: 15 * time.Millisecond}
	created := 0
	factory := func() llm.Client { created++; return llm.NewMock() }
	first, release1, err := p.acquire("owner-connection", factory)
	if err != nil {
		t.Fatal(err)
	}
	same, release2, err := p.acquire("owner-connection", factory)
	if err != nil || same != first || created != 1 {
		t.Fatal("duplicate limiter", err)
	}
	if _, _, err = p.acquire("different-connection", factory); err == nil {
		t.Fatal("active entry evicted")
	}
	release1()
	if _, _, err = p.acquire("different-connection", factory); err == nil {
		t.Fatal("queued/active borrower lost protection")
	}
	release2()
	_, release3, err := p.acquire("different-connection", factory)
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatal(created)
	}
	time.Sleep(25 * time.Millisecond)
	p.mu.Lock()
	n := len(p.entries)
	p.mu.Unlock()
	if n != 1 {
		t.Fatal("active entry expired")
	}
	release3()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		n = len(p.entries)
		p.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("idle credential entry did not expire")
}
