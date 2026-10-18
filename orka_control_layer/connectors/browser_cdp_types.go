package connectors

import (
	"context"
	"time"
)

// BrowserPageInfo identifies the leased page. Epoch changes invalidate DOM refs.
type BrowserPageInfo struct {
	LeaseID   string `json:"lease_id"`
	PageID    string `json:"page_id"`
	PageEpoch int64  `json:"page_epoch"`
}

// BrowserLease serializes commands for one operation. Close releases the remote
// queue only after cleanup acknowledgement; it is safe to call more than once.
// Commands that lose acknowledgement are never replayed.
type BrowserLease interface {
	Execute(context.Context, string, any, any) error
	Info() BrowserPageInfo
	Close() error
}

type BrowserDialer interface {
	Acquire(context.Context, GUIIdentity, time.Duration, time.Duration) (BrowserLease, error)
}

// BrowserError contains a stable machine code. An outcome_unknown error means a
// dispatched command may have changed the page; callers must not blindly retry.
type BrowserError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func (e *BrowserError) Error() string { return e.Code + ": " + e.Message }
func (e *BrowserError) Unwrap() error { return e.cause }
