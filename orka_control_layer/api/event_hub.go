package api

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// eventHub is a per-user pub/sub for lightweight UI invalidation events
// (notification / artifact / run / task). Unlike streamHub (which buffers one
// chat run's frames for reconnect), this fans small "something changed for you"
// signals out to every tab a user has open, so the frontend can refresh the
// affected resource immediately instead of waiting for its next poll tick.
type eventHub struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{} // email -> subscriber channels
}

func newEventHub() *eventHub { return &eventHub{subs: map[string]map[chan []byte]struct{}{}} }

// subscribe registers a channel for a user and returns it plus a cancel func.
func (h *eventHub) subscribe(email string) (chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	if h.subs[email] == nil {
		h.subs[email] = map[chan []byte]struct{}{}
	}
	h.subs[email][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if m := h.subs[email]; m != nil {
			if _, ok := m[ch]; ok {
				delete(m, ch)
				close(ch)
			}
			if len(m) == 0 {
				delete(h.subs, email)
			}
		}
		h.mu.Unlock()
	}
}

// publish fans a "kind" event out to all of a user's subscribers. Non-blocking:
// a slow/full subscriber simply misses this tick (its next poll still catches up).
func (h *eventHub) publish(email, kind string) {
	if email == "" {
		return
	}
	frame := []byte("data: {\"kind\":\"" + kind + "\"}\n\n")
	h.mu.Lock()
	for ch := range h.subs[email] {
		select {
		case ch <- frame:
		default:
		}
	}
	h.mu.Unlock()
}

// PublishEvent is the exported emit hook handed to the chat service (and any
// other producer) so background work can signal the user's open tabs.
func (a *API) PublishEvent(email, kind string) {
	if a.events != nil {
		a.events.publish(email, kind)
	}
}

// Events is the per-user SSE event stream. The frontend subscribes once and
// refreshes the relevant resource (bell, runs, artifacts) on each frame.
func (a *API) Events(ctx context.Context, c *app.RequestContext) {
	email := authEmail(c)
	if email == "" {
		fail(c, consts.StatusUnauthorized, "unauthorized")
		return
	}
	sub, cancel := a.events.subscribe(email)
	c.SetStatusCode(consts.StatusOK)
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	pr, pw := io.Pipe()
	c.SetBodyStream(pr, -1)
	go func() {
		defer pw.Close()
		defer cancel()
		// greet so the client knows the stream is live
		if _, err := pw.Write([]byte("data: {\"kind\":\"hello\"}\n\n")); err != nil {
			return
		}
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case frame, okk := <-sub:
				if !okk {
					return
				}
				if _, err := pw.Write(frame); err != nil {
					return
				}
			case <-ticker.C:
				if _, err := fmt.Fprint(pw, ": ping\n\n"); err != nil { // keep-alive comment
					return
				}
			}
		}
	}()
}
