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
	streamBufferSize = 256             // events retained for replay per run
	streamLinger     = 30 * time.Second // keep a finished run around for late reconnects
)

type seqFrame struct {
	seq  int64
	data []byte
}

type runStream struct {
	mu   sync.Mutex
	seq  int64
	buf  []seqFrame
	subs map[chan seqFrame]struct{}
	done bool
}

// start (re)initializes the stream for a run id and returns it.
func (h *streamHub) start(id string) *runStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	rs := &runStream{subs: map[chan seqFrame]struct{}{}}
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
	frame, err := m.SSE()
	if err != nil {
		return
	}
	rs.mu.Lock()
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
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, sf := range rs.buf {
		if sf.seq > fromSeq {
			replay = append(replay, sf)
		}
	}
	if rs.done {
		return nil, replay, true, func() {}
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
	return ch, replay, false, cancel
}
