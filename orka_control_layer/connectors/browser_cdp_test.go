package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func browserFixture(t *testing.T, serve func(*websocket.Conn)) BrowserDialer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("Origin") != "" {
			t.Error("incorrect private authentication")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		serve(conn)
	}))
	t.Cleanup(server.Close)
	return NewBrowserDialer("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/browser/cdp/ws", "fixture-token")
}
func browserReceive(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	var frame map[string]any
	if err := c.ReadJSON(&frame); err != nil {
		t.Error(err)
	}
	return frame
}
func browserReply(c *websocket.Conn, req map[string]any, kind string, extra map[string]any) {
	frame := map[string]any{"type": kind, "identity": req["identity"], "operation_id": req["operation_id"], "lease_id": "lease-fixture", "page_id": "page-fixture", "page_epoch": float64(1)}
	if id, ok := req["id"]; ok {
		frame["id"] = id
	}
	for k, v := range extra {
		frame[k] = v
	}
	_ = c.WriteJSON(frame)
}
func browserIdentity() GUIIdentity {
	return GUIIdentity{OwnerID: "owner", ConversationID: "conversation", RunID: "run"}
}

func TestBrowserLeaseAcquiresBeforeCommandsAndReleases(t *testing.T) {
	released := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		acquire := browserReceive(t, c)
		if acquire["type"] != "acquire" || acquire["operation_id"] == "" || acquire["model_config"] != nil {
			t.Errorf("invalid acquire: %v", acquire)
		}
		browserReply(c, acquire, "queued", nil)
		browserReply(c, acquire, "acquired", nil)
		for i := 1; i <= 2; i++ {
			req := browserReceive(t, c)
			if req["type"] != "command" || req["id"] != float64(i) || req["lease_id"] != "lease-fixture" || req["operation_id"] != acquire["operation_id"] {
				t.Errorf("invalid command: %v", req)
			}
			browserReply(c, req, "reply", map[string]any{"result": map[string]any{"value": i}, "page_epoch": float64(i)})
		}
		req := browserReceive(t, c)
		if req["type"] != "release" {
			t.Errorf("expected release: %v", req)
		}
		browserReply(c, req, "released", map[string]any{"page_epoch": float64(2)})
		close(released)
	})
	ctx := agent.WithMeta(context.Background(), messages.Meta{UserEmail: "owner", ConversationID: "conversation", RunID: "run"})
	identity, err := GUIIdentityFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := dialer.Acquire(ctx, identity, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		var result struct {
			Value int `json:"value"`
		}
		if err := lease.Execute(ctx, "Runtime.evaluate", map[string]any{"expression": "1"}, &result); err != nil || result.Value != i {
			t.Fatalf("result %v: %v", result, err)
		}
	}
	if lease.Info().PageEpoch != 2 {
		t.Fatal("epoch did not update")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	<-released
}

func browserErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var got *BrowserError
	if !errors.As(err, &got) || got.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestBrowserCloseInterruptsInflightCommandAndWaitsForCleanup(t *testing.T) {
	dispatched := make(chan struct{})
	cleaned := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		close(dispatched)
		cancel := browserReceive(t, c)
		if cancel["type"] != "cancel" {
			t.Errorf("want cancel: %v", cancel)
		}
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{}})
		close(cleaned)
		browserReply(c, cancel, "cancelled", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- lease.Execute(context.Background(), "Input.insertText", map[string]any{"text": "test"}, nil)
	}()
	<-dispatched
	start := time.Now()
	err = lease.Close()
	if time.Since(start) > time.Second {
		t.Error("Close did not interrupt the command promptly")
	}
	if err != nil {
		t.Fatal(err)
	}
	browserErrorCode(t, <-result, "outcome_unknown")
	select {
	case <-cleaned:
	default:
		t.Fatal("returned before cleanup acknowledgement")
	}
}
func TestBrowserLeaseRejectsForeignRepliesAndNeverReplays(t *testing.T) {
	for _, field := range []string{"identity", "operation_id", "lease_id", "page_id", "page_epoch", "id"} {
		t.Run(field, func(t *testing.T) {
			dialer := browserFixture(t, func(c *websocket.Conn) {
				req := browserReceive(t, c)
				browserReply(c, req, "acquired", nil)
				req = browserReceive(t, c)
				altered := map[string]any{"result": map[string]any{}}
				switch field {
				case "identity":
					altered[field] = map[string]any{"owner_id": "foreign"}
				case "page_epoch":
					altered[field] = 0
				case "id":
					altered[field] = 2
				default:
					altered[field] = "foreign"
				}
				browserReply(c, req, "reply", altered)
				req = browserReceive(t, c)
				if req["type"] != "cancel" {
					t.Errorf("command replayed: %v", req)
				}
				browserReply(c, req, "cancelled", nil)
			})
			lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			browserErrorCode(t, lease.Execute(context.Background(), "Input.insertText", map[string]any{"text": "once"}, nil), "outcome_unknown")
			browserErrorCode(t, lease.Execute(context.Background(), "Input.insertText", map[string]any{"text": "again"}, nil), "lease_closed")
			_ = lease.Close()
		})
	}
}
func TestBrowserQueueDoesNotConsumeExecutionBudget(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "queued", nil)
		time.Sleep(100 * time.Millisecond)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{}})
		req = browserReceive(t, c)
		browserReply(c, req, "released", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Execute(context.Background(), "Page.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserQueueTimeoutClosesPendingAcquisition(t *testing.T) {
	disconnected := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "queued", nil)
		_, _, err := c.ReadMessage()
		if err == nil {
			t.Error("acquisition timeout left connection open")
		}
		close(disconnected)
	})
	_, err := dialer.Acquire(context.Background(), browserIdentity(), 60*time.Millisecond, time.Second)
	browserErrorCode(t, err, "timeout")
	<-disconnected
}
func TestBrowserCommandCancellationIsNotSuccess(t *testing.T) {
	dispatched := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		close(dispatched)
		req = browserReceive(t, c)
		if req["type"] != "cancel" {
			t.Errorf("want cancel: %v", req)
		}
		browserReply(c, req, "cancelled", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- lease.Execute(ctx, "Input.insertText", map[string]any{"text": "one"}, nil) }()
	<-dispatched
	cancel()
	err = <-result
	browserErrorCode(t, err, "outcome_unknown")
	if !errors.Is(err, context.Canceled) {
		t.Error("cancellation cause lost")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserDisconnectCannotAcknowledgeCleanup(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		_ = browserReceive(t, c)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	browserErrorCode(t, lease.Close(), "outcome_unknown")
	browserErrorCode(t, lease.Close(), "outcome_unknown")
}
func TestBrowserPrivateResultLimitAllowsDownloadChunks(t *testing.T) {
	for _, size := range []int{110 << 10, 129 << 10} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dialer := browserFixture(t, func(c *websocket.Conn) {
				req := browserReceive(t, c)
				browserReply(c, req, "acquired", nil)
				req = browserReceive(t, c)
				browserReply(c, req, "reply", map[string]any{"result": map[string]any{"chunk": strings.Repeat("a", size)}})
				req = browserReceive(t, c)
				kind := "released"
				if size > 128<<10 {
					kind = "cancelled"
				}
				browserReply(c, req, kind, nil)
			})
			lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			err = lease.Execute(context.Background(), "Runtime.callFunctionOn", map[string]any{}, &out)
			if size > 128<<10 {
				browserErrorCode(t, err, "outcome_unknown")
			} else if err != nil {
				t.Fatal(err)
			}
			_ = lease.Close()
		})
	}
}
func TestBrowserGeneratedProtocolUsesExecutor(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		if req["method"] != "Runtime.evaluate" {
			t.Errorf("unexpected method %v", req)
		}
		params := req["params"].(map[string]any)
		if params["contextId"] != float64(7) || params["expression"] != "1+1" {
			t.Errorf("wrong params: %v", params)
		}
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{"result": map[string]any{"type": "number", "value": 2}}})
		req = browserReceive(t, c)
		browserReply(c, req, "released", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, exception, err := runtime.Evaluate("1+1").WithContextID(7).WithReturnByValue(true).Do(cdp.WithExecutor(context.Background(), lease))
	if err != nil || exception != nil || result == nil || string(result.Value) != "2" {
		t.Fatalf("generated protocol result: %v %v %v", result, exception, err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserRejectsInvalidIdentityAndPublicEndpoint(t *testing.T) {
	for _, identity := range []GUIIdentity{{}, {OwnerID: "owner", ConversationID: "conv"}, {OwnerID: strings.Repeat("o", 257), ConversationID: "conv", RunID: "run"}} {
		_, err := NewBrowserDialer("ws://127.0.0.1:1/api/v1/browser/cdp/ws", "token").Acquire(context.Background(), identity, time.Second, time.Second)
		browserErrorCode(t, err, "invalid_identity")
	}
	for _, endpoint := range []string{"ws://8.8.8.8/api/v1/browser/cdp/ws", "ws://127.0.0.1:1/api/v1/browser/cdp/ws?token=secret", "http://127.0.0.1:1"} {
		_, err := NewBrowserDialer(endpoint, "token").Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
		browserErrorCode(t, err, "unavailable")
	}
	ctx := WithGUIIdentity(context.Background(), browserIdentity())
	got, err := GUIIdentityFromContext(ctx)
	if err != nil || got != browserIdentity() {
		t.Fatal(got, err)
	}
	ctx = agent.WithMeta(ctx, messages.Meta{UserEmail: "partial-owner"})
	if _, err = GUIIdentityFromContext(ctx); err == nil {
		t.Fatal("partial trusted meta mixed with fallback")
	}
}
func TestBrowserAcquisitionValidatesScopeBeforeDispatch(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", map[string]any{"operation_id": "foreign"})
		if _, _, err := c.ReadMessage(); err == nil {
			t.Error("client sent a command on foreign acquisition")
		}
	})
	_, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	browserErrorCode(t, err, "protocol_error")
}
func TestBrowserLeaseExpiresWhenIdle(t *testing.T) {
	cleaned := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		if req["type"] != "cancel" {
			t.Errorf("want expiry cancel: %v", req)
		}
		browserReply(c, req, "cancelled", nil)
		close(cleaned)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("idle lease was not cancelled")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserCommandLimitAndLocalValidationDoNotDispatch(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		for i := 1; i <= 256; i++ {
			req = browserReceive(t, c)
			if req["id"] != float64(i) {
				t.Errorf("wrong ID: %v", req)
			}
			if _, ok := req["params"].(map[string]any); !ok {
				t.Error("nil CDP params must be encoded as an object")
			}
			browserReply(c, req, "reply", map[string]any{"result": map[string]any{}})
		}
		req = browserReceive(t, c)
		if req["type"] != "release" {
			t.Error("too many commands")
		}
		browserReply(c, req, "released", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	browserErrorCode(t, lease.Execute(context.Background(), "Target.getTargets", nil, nil), "unsupported_method")
	browserErrorCode(t, lease.Execute(context.Background(), "Runtime.evaluate", map[string]any{"expression": strings.Repeat("x", 129<<10)}, nil), "output_limit")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	browserErrorCode(t, lease.Execute(ctx, "Page.enable", nil, nil), "timeout")
	for i := 0; i < 256; i++ {
		if err = lease.Execute(context.Background(), "Page.enable", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	browserErrorCode(t, lease.Execute(context.Background(), "Page.enable", nil, nil), "command_limit")
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserConcurrentCommandsSerializeAndInfoIsSafe(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		for i := 1; i <= 24; i++ {
			req = browserReceive(t, c)
			if req["id"] != float64(i) {
				t.Errorf("non-sequential command: %v", req)
			}
			browserReply(c, req, "reply", map[string]any{"result": map[string]any{}, "page_epoch": i})
		}
		req = browserReceive(t, c)
		browserReply(c, req, "released", map[string]any{"page_epoch": 24})
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lease.Info()
			if err := lease.Execute(context.Background(), "Page.enable", nil, nil); err != nil {
				t.Error(err)
			}
			_ = lease.Info()
		}()
	}
	wg.Wait()
	if lease.Info().PageEpoch != 24 {
		t.Fatal("lost epoch update")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserScreenshotMayExceedOrdinaryResultLimit(t *testing.T) {
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{"data": strings.Repeat("A", 256<<10)}})
		req = browserReceive(t, c)
		browserReply(c, req, "released", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = lease.Execute(context.Background(), "Page.captureScreenshot", map[string]any{"format": "png"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out["data"].(string)) != 256<<10 {
		t.Fatal("screenshot silently truncated")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserCleanupAckHasBoundedWait(t *testing.T) {
	disconnected := make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		_ = browserReceive(t, c)
		_, _, _ = c.ReadMessage()
		close(disconnected)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	browserErrorCode(t, lease.Close(), "outcome_unknown")
	if time.Since(start) > 3*time.Second {
		t.Fatal("cleanup did not have a bounded timeout")
	}
	<-disconnected
}

func TestBrowserWaitingCommandHonorsItsOwnDeadline(t *testing.T) {
	dispatched, settle := make(chan struct{}), make(chan struct{})
	dialer := browserFixture(t, func(c *websocket.Conn) {
		req := browserReceive(t, c)
		browserReply(c, req, "acquired", nil)
		req = browserReceive(t, c)
		close(dispatched)
		<-settle
		browserReply(c, req, "reply", map[string]any{"result": map[string]any{}})
		req = browserReceive(t, c)
		browserReply(c, req, "released", nil)
	})
	lease, err := dialer.Acquire(context.Background(), browserIdentity(), time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { first <- lease.Execute(context.Background(), "Page.enable", nil, nil) }()
	<-dispatched
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	go func() { second <- lease.Execute(ctx, "Page.enable", nil, nil) }()
	select {
	case err := <-second:
		browserErrorCode(t, err, "timeout")
	case <-time.After(200 * time.Millisecond):
		t.Error("queued caller ignored its deadline while another command was running")
	}
	close(settle)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

// Set both variables only for an isolated fixture runtime. This does not start,
// restart or attach to the production GUI service, and makes no model calls.
func TestBrowserPythonBridgeIntegration(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("requires explicit isolated ORKA_BROWSER_TEST_WS and ORKA_BROWSER_TEST_TOKEN")
	}
	token := os.Getenv("ORKA_BROWSER_TEST_TOKEN")
	if token == "" {
		t.Fatal("fixture authentication token required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	identity := GUIIdentity{OwnerID: "transport-fixture-" + messages.NewID(), ConversationID: "fixture", RunID: "fixture-run"}
	dialer := NewBrowserDialer(endpoint, token)
	var previous BrowserPageInfo
	for i := 0; i < 2; i++ {
		lease, err := dialer.Acquire(ctx, identity, 5*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lease.Close() })
		if i > 0 && (lease.Info().PageID != previous.PageID || lease.Info().LeaseID == previous.LeaseID) {
			t.Fatal("page ownership or fresh lease invariant failed")
		}
		execCtx := cdp.WithExecutor(ctx, lease)
		if err := page.Enable().Do(execCtx); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Enable().Do(execCtx); err != nil {
			t.Fatal(err)
		}
		tree, err := page.GetFrameTree().Do(execCtx)
		if err != nil {
			t.Fatal(err)
		}
		world, err := page.CreateIsolatedWorld(tree.Frame.ID).WithWorldName("orka-browser").Do(execCtx)
		if err != nil {
			t.Fatal(err)
		}
		typed, exception, err := runtime.Evaluate("40+2").WithContextID(world).
			WithReturnByValue(true).WithAllowUnsafeEvalBlockedByCSP(false).Do(execCtx)
		if err != nil || exception != nil || typed == nil || string(typed.Value) != "42" {
			t.Fatalf("generated evaluate: %v %v %v", typed, exception, err)
		}
		expression := "document.body.textContent = 'transport fixture'; 6*7"
		if i > 0 {
			expression = "document.body.textContent"
		}
		var result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		err = lease.Execute(ctx, "Runtime.evaluate", map[string]any{"contextId": world, "expression": expression, "returnByValue": true, "allowUnsafeEvalBlockedByCSP": false}, &result)
		if err != nil {
			t.Fatal(err)
		}
		want := "42"
		if i > 0 {
			want = `"transport fixture"`
		}
		if string(result.Result.Value) != want {
			t.Fatalf("got %s want %s", result.Result.Value, want)
		}
		// A private download-sized chunk is larger than the public observation cap.
		err = lease.Execute(ctx, "Runtime.evaluate", map[string]any{"contextId": world, "expression": "'a'.repeat(110*1024)", "returnByValue": true}, &result)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Result.Value) != 110*1024+2 {
			t.Fatal("private result truncated")
		}
		previous = lease.Info()
		if err = lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
