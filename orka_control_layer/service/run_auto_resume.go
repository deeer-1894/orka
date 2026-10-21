package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
)

// A provider outage outlasts any retry a single call can afford. The transport
// waits out a 429 for up to 45s and ADK retries the call twice more, which
// covers a burst limit but not a provider that is overloaded for ten minutes —
// and with unattended triggers, the run that hits that at 3am has nobody to
// press 继续任务. Those runs already keep a journal and can be continued without
// redoing their work; what they lacked was someone to ask.
//
// So a run that died on a transient provider error schedules its own
// continuation, a few minutes out. The schedule is a field on the run record,
// swept by the same loop that reaps orphaned runs, so a restart in between
// costs nothing: the next process finds the due record and carries on.

// autoResumeDelays is the backoff before each automatic continuation in a
// chain; its length caps the chain. Minutes, not seconds: anything shorter is
// already covered by the in-call retries, which failed.
var autoResumeDelays = []time.Duration{2 * time.Minute, 5 * time.Minute, 15 * time.Minute}

// autoResumeSweepInterval is how often due resumes are looked for. Much finer
// than the reaper's pass so a two-minute backoff is not stretched to four.
var autoResumeSweepInterval = 20 * time.Second

// autoResumeBatch bounds the resumes one sweep may start, so a provider
// recovering from a long outage is not met with every stalled run at once.
const autoResumeBatch = 8

// transientProviderFailure reports whether a run died because the model
// provider was temporarily unable to serve it — rate limited, overloaded, or
// failing with a 5xx — as opposed to anything waiting would not fix.
//
// Stricter than llm.IsTransient, which counts every non-API error as a network
// blip: that is right for a single model call, but a run's error can come from
// anywhere, and resuming a run that failed on a bad checkpoint only fails again.
func transientProviderFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || llm.IsCallLimit(err) {
		return false
	}
	if _, exhausted := llm.QuotaExhaustion(err); exhausted {
		return false // a spent quota does not come back in fifteen minutes
	}
	// ADK's retry wrapper unwraps to its sentinel, not the cause, so the
	// provider error behind "exceeds max retries" has to be fetched by hand.
	var exhausted *adk.RetryExhaustedError
	if errors.As(err, &exhausted) && exhausted.LastErr != nil {
		return llm.IsTransient(context.Background(), exhausted.LastErr)
	}
	var ae *llm.APIError
	if errors.As(err, &ae) {
		return llm.IsTransient(context.Background(), ae)
	}
	// Errors that crossed a string boundary (graph node errors, providers that
	// report overload in an SSE error frame) are recognised by their text.
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"llm status 408", "llm status 425", "llm status 429", "llm status 500", "llm status 502",
		"llm status 503", "llm status 504", "llm status 529",
		"too many requests", "rate limit", "overload", "server busy", "service unavailable", "temporarily unavailable",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// autoResumeEligible decides, before the run is finalized, whether a failure
// would be continued automatically. Never for a run the user stopped, and
// never past the end of the backoff schedule.
func (s *ChatService) autoResumeEligible(ctx context.Context, req ChatRunRequest, err error) bool {
	if s.Msg == nil || s.Msg.Store == nil || req.UserEmail == "" || req.ResumeKey != "" {
		return false
	}
	if ctx.Err() != nil || req.autoResumes < 0 || req.autoResumes >= len(autoResumeDelays) {
		return false
	}
	return transientProviderFailure(err)
}

// scheduleAutoResume records when runID will be continued. Returns the due time
// and whether the record accepted it (it must still be resumable).
func (s *ChatService) scheduleAutoResume(runID string, req ChatRunRequest) (time.Time, bool) {
	at := time.Now().Add(autoResumeDelays[req.autoResumes])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := s.Msg.Store.ScheduleRunAutoResume(ctx, runID, at.UnixMilli(), req.autoResumes+1)
	if err != nil || !ok {
		if s.Log != nil {
			s.Log.Warn("auto resume not scheduled", "run_id", runID, "err", err)
		}
		return time.Time{}, false
	}
	if s.Log != nil {
		s.Log.Info("auto resume scheduled", "run_id", runID, "attempt", req.autoResumes+1, "at", at.Format(time.RFC3339))
	}
	if s.OnEvent != nil {
		s.OnEvent(req.UserEmail, "run") // the thread's status line reads the record
	}
	return at, true
}

// sweepAutoResumes starts every due automatic resume, up to a batch. The
// returned group completes when the resumes it started have finished, which
// only tests wait for.
func (s *ChatService) sweepAutoResumes(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	if s.Msg == nil || s.Msg.Store == nil {
		return &wg
	}
	for i := 0; i < autoResumeBatch; i++ {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		rec, err := s.Msg.Store.ClaimDueAutoResume(c, time.Now().UnixMilli())
		cancel()
		if err != nil {
			if !errors.Is(err, db.ErrNotFound) && s.Log != nil {
				s.Log.Warn("auto resume sweep failed", "err", err)
			}
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.autoResume(rec)
		}()
	}
	return &wg
}

// autoResume continues one claimed run exactly as the 继续任务 button would,
// publishing into the conversation's stream so an open thread can join it.
func (s *ChatService) autoResume(rec *db.RunRecord) {
	admitted, release, err := s.AdmitExecution(context.Background(), rec.OwnerEmail, rec.ConversationID, rec.TaskID)
	if err != nil {
		// Something else is executing in this conversation. Whatever it is
		// started after the failure, so it supersedes the stale continuation;
		// the run remains resumable by hand.
		if s.Log != nil {
			s.Log.Info("auto resume skipped", "run_id", rec.RunID, "err", err)
		}
		return
	}
	defer release()
	prepared, err := s.PrepareResumeRun(admitted, rec.RunID, rec.OwnerEmail, ResumeOptions{autoResumes: rec.AutoResumeAttempt})
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("auto resume rejected", "run_id", rec.RunID, "err", err)
		}
		return
	}
	publish, done := func(messages.Message) {}, func() {}
	if s.OpenStream != nil {
		publish, done = s.OpenStream(rec.ConversationID, ExecutionID(admitted))
	}
	defer done()
	if s.Log != nil {
		s.Log.Info("auto resume started", "run_id", rec.RunID, "attempt", rec.AutoResumeAttempt)
	}
	if _, err := prepared(admitted, publish); err != nil && s.Log != nil {
		s.Log.Warn("auto resume failed", "run_id", rec.RunID, "err", err)
	}
}
