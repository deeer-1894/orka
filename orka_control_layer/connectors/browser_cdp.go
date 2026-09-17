package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/gorilla/websocket"
	"github.com/orka-oss/orka_core/messages"
)

const browserCleanupTimeout = 2 * time.Second

// Private replies allow 128 KiB results for bounded download chunks. The larger
// frame ceiling is only for a <=16 MiB screenshot encoded as base64. Public DOM
// and evaluate observation limits are enforced by the browser engine.
const browserReadLimit = 24 << 20
const browserResultLimit = 128 << 10

type browserDialer struct{ endpoint, token string }

// NewBrowserDialer creates a reusable dialer without opening a connection.
// endpoint is the full authenticated private browser bridge WS URL.
func NewBrowserDialer(endpoint, token string) BrowserDialer {
	return &browserDialer{endpoint, token}
}

type browserEnvelope struct {
	Type        string      `json:"type"`
	Identity    GUIIdentity `json:"identity"`
	OperationID string      `json:"operation_id"`
	BrowserPageInfo
	ID               int64           `json:"id,omitempty"`
	Method           string          `json:"method,omitempty"`
	Params           json.RawMessage `json:"params,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	Error            *BrowserError   `json:"error,omitempty"`
	QueueTimeout     float64         `json:"queue_timeout,omitempty"`
	ExecutionTimeout float64         `json:"execution_timeout,omitempty"`
}
type browserRead struct {
	frame browserEnvelope
	err   error
	size  int
}
type browserLease struct {
	interruptCtx context.Context
	interrupt    context.CancelFunc
	conn         *websocket.Conn
	identity     GUIIdentity
	operation    string
	incoming     chan browserRead
	done         chan struct{}
	gate         chan struct{} // Owns commands, writes and cleanup; waits respect context.
	infoMu       sync.RWMutex
	info         BrowserPageInfo
	nextID       int64
	closed       bool
	closeErr     error
	ctx          context.Context
	cancel       context.CancelFunc
}

var _ cdp.Executor = (*browserLease)(nil)

func browserFailure(code, message string, cause error) error {
	return &BrowserError{Code: code, Message: message, cause: cause}
}
func (d *browserDialer) Acquire(ctx context.Context, identity GUIIdentity, queue, execution time.Duration) (BrowserLease, error) {
	for _, s := range []string{identity.OwnerID, identity.ConversationID, identity.RunID} {
		if strings.TrimSpace(s) == "" || len(s) > 256 {
			return nil, browserFailure("invalid_identity", "trusted owner/conversation/run required", nil)
		}
	}
	if queue == 0 {
		queue = 30 * time.Second
	}
	if execution == 0 {
		execution = 45 * time.Second
	}
	if queue < 0 || queue > 60*time.Second || execution <= 0 || execution > 60*time.Second {
		return nil, browserFailure("invalid_timeout", "queue and execution must be positive and at most 60 seconds", nil)
	}
	acquireCtx, cancel := context.WithTimeout(ctx, queue)
	defer cancel()
	conn, err := dialGUI(acquireCtx, d.endpoint, d.token)
	if err != nil {
		return nil, browserFailure("unavailable", "browser private connection failed", err)
	}
	l := &browserLease{conn: conn, identity: identity, operation: messages.NewID(), incoming: make(chan browserRead, 1), done: make(chan struct{}), gate: make(chan struct{}, 1)}
	l.gate <- struct{}{}
	conn.SetReadLimit(browserReadLimit)
	go l.readLoop()
	request := l.envelope("acquire")
	request.QueueTimeout = queue.Seconds()
	request.ExecutionTimeout = execution.Seconds()
	if err = l.write(acquireCtx, request); err == nil {
		for {
			var r browserRead
			r, err = l.receive(acquireCtx)
			if err != nil {
				break
			}
			if r.size > 128<<10 || !l.scopeMatches(r.frame, false) {
				err = browserFailure("protocol_error", "invalid acquire envelope", nil)
				break
			}
			switch r.frame.Type {
			case "queued":
				continue
			case "error":
				err = l.remoteError(r.frame)
			case "acquired":
				if r.frame.Error != nil || r.frame.LeaseID == "" || r.frame.PageID == "" || r.frame.PageEpoch < 1 {
					err = browserFailure("protocol_error", "invalid acquired page", nil)
					break
				}
				l.info = r.frame.BrowserPageInfo
				l.ctx, l.cancel = context.WithTimeout(ctx, execution)
				l.interruptCtx, l.interrupt = context.WithCancel(context.Background())
				go func() {
					select {
					case <-l.ctx.Done():
						<-l.gate
						if !l.closed {
							_ = l.finish("cancel")
						}
						l.gate <- struct{}{}
					case <-l.done:
					}
				}()
				return l, nil
			default:
				err = browserFailure("protocol_error", "unexpected acquisition reply", nil)
			}
			break
		}
	}
	// Disconnect cancels a queued acquisition on the server.
	_ = conn.Close()
	close(l.done)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, browserFailure("timeout", "browser acquisition cancelled or timed out", err)
	}
	return nil, err
}
func (l *browserLease) readLoop() {
	for {
		kind, data, err := l.conn.ReadMessage()
		r := browserRead{err: err, size: len(data)}
		if err == nil {
			if kind != websocket.TextMessage {
				r.err = fmt.Errorf("expected JSON text frame")
			} else {
				r.err = json.Unmarshal(data, &r.frame)
			}
		}
		select {
		case l.incoming <- r:
		case <-l.done:
			return
		}
		if r.err != nil {
			return
		}
	}
}
func (l *browserLease) Info() BrowserPageInfo {
	l.infoMu.RLock()
	defer l.infoMu.RUnlock()
	return l.info
}
func (l *browserLease) envelope(kind string) browserEnvelope {
	return browserEnvelope{Type: kind, Identity: l.identity, OperationID: l.operation, BrowserPageInfo: l.Info()}
}
func (l *browserLease) scopeMatches(f browserEnvelope, leased bool) bool {
	if f.Identity != l.identity || f.OperationID != l.operation {
		return false
	}
	if !leased {
		return true
	}
	info := l.Info()
	return f.LeaseID == info.LeaseID && f.PageID == info.PageID && f.PageEpoch >= info.PageEpoch
}
func (l *browserLease) write(ctx context.Context, f browserEnvelope) error {
	data, err := json.Marshal(f)
	if err != nil {
		return browserFailure("invalid_params", "cannot encode browser command", err)
	}
	limit := 128 << 10
	if f.Type == "command" && f.Method == "Orka.previewHTML" {
		limit = 1408 << 10
	}
	if len(data) > limit {
		return browserFailure("output_limit", "browser command exceeds message limit", nil)
	}
	deadline := time.Now().Add(browserCleanupTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = l.conn.SetWriteDeadline(deadline)
	return l.conn.WriteMessage(websocket.TextMessage, data)
}
func (l *browserLease) receive(ctx context.Context) (browserRead, error) {
	select {
	case r := <-l.incoming:
		return r, r.err
	case <-ctx.Done():
		return browserRead{}, ctx.Err()
	}
}
func (l *browserLease) remoteError(f browserEnvelope) error {
	if f.Error == nil || f.Error.Code == "" || len(f.Error.Code) > 128 || len(f.Error.Message) > 4096 {
		return browserFailure("protocol_error", "invalid browser error", nil)
	}
	return f.Error
}

func (l *browserLease) Execute(ctx context.Context, method string, params, result any) error {
	if l.interruptCtx.Err() != nil {
		return browserFailure("lease_closed", "browser lease is closing", nil)
	}
	select {
	case <-ctx.Done():
		return browserFailure("timeout", "command cancelled before dispatch", ctx.Err())
	case <-l.interruptCtx.Done():
		return browserFailure("lease_closed", "browser lease is closing", nil)
	case <-l.ctx.Done():
		return browserFailure("timeout", "browser lease expired before dispatch", l.ctx.Err())
	case <-l.gate:
	}
	defer func() { l.gate <- struct{}{} }()
	if l.closed {
		return browserFailure("lease_closed", "browser lease is closed", l.closeErr)
	}
	if l.interruptCtx.Err() != nil {
		return browserFailure("lease_closed", "browser lease is closing", nil)
	}
	if err := ctx.Err(); err != nil {
		return browserFailure("timeout", "command cancelled before dispatch", err)
	}
	if err := l.ctx.Err(); err != nil {
		_ = l.finish("cancel")
		return browserFailure("timeout", "browser lease expired before dispatch", err)
	}
	if l.nextID >= 256 {
		return browserFailure("command_limit", "browser lease allows at most 256 commands", nil)
	}
	if !browserMethods[method] {
		return browserFailure("unsupported_method", "browser method is not allowed", nil)
	}
	if params == nil {
		params = map[string]any{}
	}
	encoded, err := jsonv2.Marshal(params)
	if err != nil {
		return browserFailure("invalid_params", "cannot encode CDP parameters", err)
	}
	limit := 120 << 10
	if method == "Orka.previewHTML" {
		limit = 1400 << 10
	}
	if len(encoded) > limit {
		return browserFailure("output_limit", "CDP parameters exceed message limit", nil)
	}
	l.nextID++
	f := l.envelope("command")
	f.ID = l.nextID
	f.Method = method
	f.Params = encoded
	commandCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.ctx, cancel)
	defer stop()
	stopInterrupt := context.AfterFunc(l.interruptCtx, cancel)
	defer stopInterrupt()
	defer cancel()
	if err = l.write(commandCtx, f); err != nil {
		_ = l.finish("cancel")
		return browserFailure("outcome_unknown", "command delivery was not acknowledged", err)
	}
	r, err := l.receive(commandCtx)
	if err == nil {
		if !l.scopeMatches(r.frame, true) || r.frame.Type != "reply" || r.frame.ID != f.ID {
			err = browserFailure("protocol_error", "browser reply scope or ID mismatch", nil)
		} else if method != "Page.captureScreenshot" && r.size > browserResultLimit+4096 {
			err = browserFailure("output_limit", "browser reply exceeds envelope limit", nil)
		} else {
			l.infoMu.Lock()
			l.info = r.frame.BrowserPageInfo
			l.infoMu.Unlock()
			if r.frame.Error != nil {
				if len(r.frame.Result) > 0 {
					err = browserFailure("protocol_error", "reply contains both result and error", nil)
				} else {
					return l.remoteError(r.frame)
				}
			} else if len(r.frame.Result) == 0 {
				err = browserFailure("protocol_error", "missing CDP result", nil)
			} else if method != "Page.captureScreenshot" && len(r.frame.Result) > browserResultLimit {
				err = browserFailure("output_limit", "CDP result exceeds 128 KiB", nil)
			} else if result != nil {
				err = jsonv2.Unmarshal(r.frame.Result, result)
			} else {
				return nil
			}
			if err == nil {
				return nil
			}
		}
	}
	_ = l.finish("cancel")
	return browserFailure("outcome_unknown", "command result could not be confirmed", err)
}

// Close waits for bounded cleanup acknowledgement, including after cancellation.
func (l *browserLease) Close() error {
	l.interrupt()
	<-l.gate
	defer func() { l.gate <- struct{}{} }()
	if l.closed {
		return l.closeErr
	}
	kind := "release"
	if l.ctx.Err() != nil {
		kind = "cancel"
	}
	return l.finish(kind)
}
func (l *browserLease) finish(kind string) error {
	if l.closed {
		return l.closeErr
	}
	defer func() { l.closed = true; l.interrupt(); l.cancel(); _ = l.conn.Close(); close(l.done) }()
	ctx, cancel := context.WithTimeout(context.Background(), browserCleanupTimeout)
	defer cancel()
	err := l.write(ctx, l.envelope(kind))
	for err == nil {
		var r browserRead
		r, err = l.receive(ctx)
		if err != nil {
			break
		}
		if !l.scopeMatches(r.frame, true) || r.size > 128<<10 {
			err = fmt.Errorf("invalid cleanup scope")
			break
		}
		// A cancelled command's result may precede cleanup, but no other ID may.
		if kind == "cancel" && r.frame.Type == "reply" && r.frame.ID == l.nextID {
			continue
		}
		if r.frame.Type == map[string]string{"release": "released", "cancel": "cancelled"}[kind] && r.frame.Error == nil {
			return nil
		}
		err = fmt.Errorf("cleanup not acknowledged")
	}
	l.closeErr = browserFailure("outcome_unknown", "browser cleanup was not acknowledged", err)
	return l.closeErr
}

var browserMethods = map[string]bool{
	"Orka.previewHTML": true,
	"Page.enable":      true, "Page.getFrameTree": true, "Page.createIsolatedWorld": true, "Page.navigate": true, "Page.captureScreenshot": true,
	"Runtime.enable": true, "Runtime.evaluate": true, "Runtime.callFunctionOn": true, "Runtime.getProperties": true, "Runtime.releaseObject": true, "Runtime.releaseObjectGroup": true,
	"DOM.getDocument": true, "DOM.querySelector": true, "DOM.describeNode": true, "DOM.resolveNode": true, "DOM.scrollIntoViewIfNeeded": true, "DOM.getBoxModel": true,
	"Input.dispatchMouseEvent": true, "Input.dispatchKeyEvent": true, "Input.insertText": true,
}
