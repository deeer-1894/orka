package service

import (
	"context"
	"strings"
	"time"
)

// tool_retry.go — the tool boundary's own resilience.
//
// Model calls have had retry and failover for a while; tool calls had nothing.
// The gap showed: 48 tool failures were `transport error: failed to send
// request`, the MCP connection to tools_server dropping, and every one was
// handed to the model as a permanent failure. Measured per tool, that is why
// the LOCAL filesystem tools were the least reliable in the whole system —
// file_write failed 22.4% of the time against web_search's 1.2%, not because
// writing a file is hard but because nothing retried the socket.
//
// The second half of this file is the error text. Every failure used to be
// wrapped in the same 90-character sentence telling the model to "try a
// different approach, another tool, or proceed without this result". For a
// dropped connection that advice is backwards — the right move is to try the
// same thing again — and for a permission error it is a waste, since no other
// approach will help either. The wrapper also buried the actual cause behind
// its own boilerplate, on every failure, in the model's context.

// toolFailureKind classifies a tool error by what the caller should DO about it.
type toolFailureKind int

const (
	// failTransient is infrastructure: a dropped socket, a timeout, a 5xx. The
	// same call will probably work.
	failTransient toolFailureKind = iota
	// failAuth is a credential problem. Retrying the call is useless, but the
	// caller is not forbidden — the credential needs refreshing.
	failAuth
	// failDenied is a real permission decision. No retry, no workaround.
	failDenied
	// failCancelled means the call was abandoned from ABOVE — the run was
	// stopped, or eino cancelled this branch while retrying a failed model
	// stream. Nothing is wrong with the tool, and retrying under the same dead
	// context can only fail again. This is not hypothetical: the top killer of
	// long runs here is the provider dropping a stream mid-generation, and every
	// tool call in flight when that happens surfaces as a transport error.
	failCancelled
	// failOther is everything else: bad arguments, a missing file, upstream
	// rejection. The model's judgment is the right tool here.
	failOther
)

// transientMarkers are substrings that identify an infrastructure failure. Text
// matching is unavoidable: these errors arrive as strings from an MCP peer, with
// no typed error crossing the wire.
var transientMarkers = []string{
	"transport error",
	"failed to send request",
	"connection refused",
	"connection reset",
	"broken pipe",
	"EOF",
	"i/o timeout",
	"context deadline exceeded",
	"temporary failure",
	"no such host",
	"502", "503", "504",
}

func classifyToolError(msg string) toolFailureKind {
	switch {
	// Checked before the transient markers on purpose: a cancelled call arrives
	// wrapped as "transport error: … context canceled", and treating it as a
	// dropped socket would retry it twice against an already-dead context.
	case strings.Contains(msg, "context canceled"), strings.Contains(msg, "operation was canceled"):
		return failCancelled
	case strings.Contains(msg, "auth failed"), strings.Contains(msg, "token expired"):
		return failAuth
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "missing scope"):
		return failDenied
	}
	low := strings.ToLower(msg)
	for _, m := range transientMarkers {
		if strings.Contains(low, strings.ToLower(m)) {
			return failTransient
		}
	}
	return failOther
}

const (
	// toolRetries is how many EXTRA attempts a transient failure gets. Two is
	// enough for a dropped connection to be re-established without turning a
	// genuinely unreachable service into a long stall.
	toolRetries = 2
	// toolRetryBackoff is the first pause between attempts; it doubles.
	toolRetryBackoff = 400 * time.Millisecond
)

// retryTransient runs a tool call, repeating it while the failure looks like
// infrastructure. Only errors are retried, never a successful result, so a tool
// that reports failure in its OUTPUT (rather than as an error) is untouched.
//
// Retries are confined to transient failures on purpose. A blanket retry would
// double-apply writes: this runs tools with side effects, and MCP gives no
// idempotency key to make that safe.
// Returns the number of RETRIES actually performed, so the message handed to
// the model can state what happened instead of assuming — an error that claims
// "already retried" when it did not is exactly the kind of misleading text this
// file exists to remove.
func retryTransient(ctx context.Context, call func() (string, error)) (string, error, int) {
	out, err := call()
	retries := 0
	for attempt := 0; err != nil && attempt < toolRetries; attempt++ {
		if ctx.Err() != nil {
			return out, err, retries // the run is going away; stop trying
		}
		if isInterruptErr(err) || classifyToolError(err.Error()) != failTransient {
			return out, err, retries
		}
		select {
		case <-ctx.Done():
			return out, err, retries
		case <-time.After(toolRetryBackoff << attempt):
		}
		out, err = call()
		retries++
	}
	return out, err, retries
}

// toolErrorMessage renders a failure for the model, saying what to do about it
// rather than repeating one generic sentence. Kept short: this text lands in the
// context on every failure, and the cause matters more than the advice.
func toolErrorMessage(name string, err error, retries int) string {
	msg := err.Error()
	switch classifyToolError(msg) {
	case failCancelled:
		return "tool error (" + name + ", the call was cancelled upstream — the tool is fine and " +
			"nothing was retried; if the run is still going, simply call it again): " + msg
	case failTransient:
		tried := "not retried"
		if retries > 0 {
			tried = "retried " + itoa(retries) + "x and still failing"
		}
		return "tool error (" + name + ", infrastructure — " + tried +
			"; try another tool or continue without it): " + msg
	case failAuth:
		return "tool error (" + name + ", credentials — not a permission problem and not worth retrying " +
			"this turn; continue without it and report it): " + msg
	case failDenied:
		return "tool error (" + name + ", not permitted — do not retry this tool or look for a way around " +
			"it; continue without it and say so): " + msg
	default:
		return "tool error (" + name + ", recoverable — adjust the arguments, try another tool, " +
			"or proceed without this result): " + msg
	}
}
