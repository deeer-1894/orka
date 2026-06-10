// Package ws provides a reconnecting WebSocket client: the stable "event
// channel" the control layer depends on to talk to remote executors, rather
// than any one executor implementation.
package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrClosed is returned by Send after Close.
var ErrClosed = errors.New("ws: client closed")

// Client maintains a single logical connection with exponential-backoff
// reconnect and an outbound queue. Inbound frames are delivered to OnMessage.
type Client struct {
	url       string
	onMessage func([]byte)
	onConnect func()

	mu     sync.Mutex
	conn   *websocket.Conn
	closed bool
	cancel context.CancelFunc

	sendCh     chan []byte
	maxBackoff time.Duration
	headers    http.Header
}

// Option configures a Client.
type Option func(*Client)

// WithOnMessage sets the inbound frame callback.
func WithOnMessage(f func([]byte)) Option { return func(c *Client) { c.onMessage = f } }

// WithOnConnect sets a callback invoked on each successful (re)connect.
func WithOnConnect(f func()) Option { return func(c *Client) { c.onConnect = f } }

// WithHeaders sets HTTP headers sent on the WebSocket handshake (e.g. an auth
// token for the executor).
func WithHeaders(h http.Header) Option { return func(c *Client) { c.headers = h } }

// NewClient creates a client for url (call Start to connect).
func NewClient(url string, opts ...Option) *Client {
	c := &Client{url: url, sendCh: make(chan []byte, 64), maxBackoff: 30 * time.Second}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Start launches the connection-maintenance loop until ctx is done or Close.
func (c *Client) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go c.loop(ctx)
}

func (c *Client) loop(ctx context.Context) {
	const base = 500 * time.Millisecond
	backoff := base
	for {
		if ctx.Err() != nil {
			return
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, c.headers)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > c.maxBackoff {
				backoff = c.maxBackoff
			}
			continue
		}
		backoff = base
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()
		if c.onConnect != nil {
			c.onConnect()
		}
		c.serve(ctx, conn)
		_ = conn.Close()
	}
}

func (c *Client) serve(ctx context.Context, conn *websocket.Conn) {
	cctx, ccancel := context.WithCancel(ctx)
	defer ccancel()

	go func() { // writer
		for {
			select {
			case <-cctx.Done():
				return
			case msg := <-c.sendCh:
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					ccancel()
					return
				}
			}
		}
	}()

	for { // reader
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if c.onMessage != nil {
			c.onMessage(data)
		}
	}
}

// Send queues a frame for delivery (blocks up to 5s if the queue is full).
func (c *Client) Send(b []byte) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	select {
	case c.sendCh <- b:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("ws: send timeout (queue full)")
	}
}

// Close stops reconnection and tears down the current connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}
