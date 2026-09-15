package api

import (
	"sync"
	"time"

	"github.com/orka-oss/orka_core/messages"
)

// streamHub lets an SSE client reconnect mid-run and replay the events it
// missed. The chat run is already decoupled from the HTTP request (it runs on a
// background context), so it keeps producing events after a disconnect; the hub
// buffers them per conversation and fans them out to live subscribers.
type streamHub struct {
	mu   sync.Mutex
	runs map[string]*runStream
}

func newStreamHub() *streamHub { return &streamHub{runs: map[string]*runStream{}} }

const (
	streamBufferSize = 256              // events retained for replay per run
	streamLinger     = 30 * time.Second // keep a finished run around for late reconnects
)

type seqFrame struct {
	seq  int64
	data []byte
}

type runStream struct {
	executionID string
	mu          sync.Mutex
	seq         int64
	buf         []seqFrame
	subs        map[chan seqFrame]struct{}
	done        bool
}

// start (re)initializes the stream for a run id and returns it.
func (h *streamHub) start(id string, executionIDs ...string) *runStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	rs := &runStream{subs: map[chan seqFrame]struct{}{}}
	if len(executionIDs) > 0 {
		rs.executionID = executionIDs[0]
	}
	h.runs[id] = rs
	return rs
}

func (h *streamHub) get(id string) *runStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[id]
}

// publish assigns a sequence number, buffers the frame, and fans it out.
func (h *streamHub) publish(id string, m messages.Message) {
	rs := h.get(id)
	if rs == nil {
		return
	}
	rs.publish(m)
}

// publish belongs to an execution instance, never a conversation lookup.
func (rs *runStream) publish(m messages.Message) {
	frame, err := m.SSE()
	if err != nil {
		return
	}
	rs.mu.Lock()
	if rs.done {
		rs.mu.Unlock()
		return
	}
	rs.seq++
	sf := seqFrame{seq: rs.seq, data: frame}
	rs.buf = append(rs.buf, sf)
	if len(rs.buf) > streamBufferSize {
		rs.buf = rs.buf[len(rs.buf)-streamBufferSize:]
	}
	for ch := range rs.subs {
		select {
		case ch <- sf:
		default: // slow consumer: drop it; it will replay from buffer on reconnect
			close(ch)
			delete(rs.subs, ch)
		}
	}
	rs.mu.Unlock()
}

// finish closes all subscribers and evicts the run after a linger window.
func (h *streamHub) finish(id string) {
	rs := h.get(id)
	if rs == nil {
		return
	}
	h.finishStream(id, rs)
}

func (h *streamHub) finishStream(id string, rs *runStream) {
	rs.mu.Lock()
	rs.done = true
	for ch := range rs.subs {
		close(ch)
		delete(rs.subs, ch)
	}
	rs.mu.Unlock()
	time.AfterFunc(streamLinger, func() {
		h.mu.Lock()
		if h.runs[id] == rs { // not replaced by a newer run
			delete(h.runs, id)
		}
		h.mu.Unlock()
	})
}

// subscribe returns a channel that first replays buffered frames with seq >
// fromSeq, then receives live frames. The bool reports whether the run is
// already finished (caller should drain replay then stop). cancel detaches.
func (rs *runStream) subscribe(fromSeq int64) (ch chan seqFrame, replay []seqFrame, done bool, cancel func()) {
	ch, replay, done, cancel, _ = rs.subscribeChecked(fromSeq, true)
	return
}

// Gap detection and subscription share one lock, so rollover cannot occur
// between deciding a cursor is valid and collecting its replay.
func (rs *runStream) subscribeChecked(fromSeq int64, reconcile bool) (ch chan seqFrame, replay []seqFrame, done bool, cancel func(), gap bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	gap = rs.gapLocked(fromSeq)
	if gap && !reconcile {
		return nil, nil, false, func() {}, true
	}
	if gap {
		fromSeq = 0
	}
	for _, sf := range rs.buf {
		if sf.seq > fromSeq {
			replay = append(replay, sf)
		}
	}
	if rs.done {
		return nil, replay, true, func() {}, false
	}
	ch = make(chan seqFrame, 512)
	rs.subs[ch] = struct{}{}
	cancel = func() {
		rs.mu.Lock()
		if _, ok := rs.subs[ch]; ok {
			delete(rs.subs, ch)
			close(ch)
		}
		rs.mu.Unlock()
	}
	return ch, replay, false, cancel, false
}

// A missing interval or cursor from a different execution requires durable
// history reconciliation instead of silently skipping deltas.
func (rs *runStream) hasGap(from int64) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.gapLocked(from)
}
func (rs *runStream) gapLocked(from int64) bool {
	return from < 0 || from > rs.seq || (len(rs.buf) > 0 && from < rs.buf[0].seq-1)
}
