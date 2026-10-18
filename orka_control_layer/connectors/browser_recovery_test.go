package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBrowserReadCancellationAndTimeoutRaceStillAcknowledgeCleanup(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Orka.getPageState", "Orka.observe", "Runtime.evaluate", "Input.insertText"} {
		t.Run(method, func(t *testing.T) {
			dispatched := make(chan struct{})
			dialer := browserFixture(t, func(c *websocket.Conn) {
				req := browserReceive(t, c)
				browserReply(c, req, "acquired", nil)
				_ = browserReceive(t, c)
				close(dispatched)
				cancel := browserReceive(t, c)
				if cancel["type"] != "cancel" {
					t.Error("failed command was replayed")
				}
				browserReply(c, cancel, "error", map[string]any{"error": map[string]any{"code": "timeout", "message": "execution deadline"}})
				browserReply(c, cancel, "cancelled", map[string]any{"page_epoch": 2})
			})
			lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- lease.Execute(ctx, method, nil, nil) }()
			<-dispatched
			cancel()
			code := "outcome_unknown"
			if method == "Page.getFrameTree" || method == "Orka.getPageState" || method == "Orka.observe" {
				code = "observation_failed"
			}
			browserErrorCode(t, <-done, code)
			if err := lease.Close(); err != nil || lease.Info().PageEpoch != 2 {
				t.Fatalf("timeout race lost cleanup acknowledgement: info=%+v err=%v", lease.Info(), err)
			}
		})
	}
}

func TestObservationDeliveryReceiptFollowsReleaseAcknowledgement(t *testing.T) {
	receipted := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{"result": map[string]any{"value": map[string]any{"ok": true}}}})
		req = browserReceive(t, c)
		if req["type"] != "release" {
			t.Error("observation acknowledged before release")
		}
		browserReply(c, req, "released", nil)
		req = browserReceive(t, c)
		if req["type"] != "observation_ack" {
			t.Error("observation delivery receipt missing")
		}
		close(receipted)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Execute(context.Background(), "Orka.observe", map[string]any{"operation": "commit_observation"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	<-receipted
}
