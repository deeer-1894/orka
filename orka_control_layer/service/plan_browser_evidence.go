package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/orka-oss/orka_core/messages"
)

// The tracker owns attribution and evidence. Transport and browser lifecycle
// stay outside it. Sequence identifies a call; generation establishes causality.
type planBrowserReceipt struct {
	ID              string `json:"id"`
	Sequence        int    `json:"sequence"`
	StartedAfter    int    `json:"started_after,omitempty"`
	Completed       int    `json:"completed_generation,omitempty"`
	Action          string `json:"action"`
	URL             string `json:"url,omitempty"`
	TargetURL       string `json:"target_url,omitempty"`
	SourceURL       string `json:"source_url,omitempty"`
	PageID          string `json:"page_id,omitempty"`
	PageEpoch       int64  `json:"page_epoch,omitempty"`
	Follows         string `json:"follows,omitempty"`
	Uncontended     bool   `json:"uncontended,omitempty"`
	ObservationOnly bool   `json:"observation_only,omitempty"`
	Code            string `json:"code,omitempty"`
	OK              bool   `json:"ok"`
	Observed        bool   `json:"observed,omitempty"`
}
type planBrowserEvidence struct {
	Sequence        int                           `json:"sequence"`
	Generation      int                           `json:"generation,omitempty"`
	Observed        bool                          `json:"observed,omitempty"`
	LastObservedURL string                        `json:"last_observed_url,omitempty"`
	ContinuationID  string                        `json:"continuation_id,omitempty"`
	Pending         map[int]planBrowserReceipt    `json:"pending,omitempty"`
	Receipts        []planBrowserReceipt          `json:"receipts,omitempty"`
	Failures        map[string]planBrowserReceipt `json:"failures,omitempty"`
}
type planBrowserCall struct {
	key     string
	receipt planBrowserReceipt
}

// Resolve ownership and register Pending under ONE lock. A concurrent plan
// update either sees this call in flight, or closes the step before admission.
func (p *planTracker) beginBrowserCall(id string, args map[string]any) (planBrowserCall, error) {
	if p == nil {
		return planBrowserCall{}, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.steps) == 0 {
		return planBrowserCall{}, nil
	}
	key := ""
	for _, step := range p.steps {
		if id != "" && step.ID == id && step.Status != "done" {
			key = planStepKey(step)
			break
		}
		if id == "" && step.Status == "active" {
			if key != "" {
				key = ""
				break
			}
			key = planStepKey(step)
		}
	}
	if key == "" {
		return planBrowserCall{}, errors.New("identify an unfinished plan step with plan_step_id before this browser action; do not guess or change completed steps")
	}
	if p.browser == nil {
		p.browser = make(map[string]planBrowserEvidence)
	}
	state := p.browser[key]
	digest := sha256.Sum256([]byte(key))
	action, _ := args["action"].(string)
	r := planBrowserReceipt{ID: fmt.Sprintf("b-%x-%d", digest[:4], state.Sequence+1), Sequence: state.Sequence + 1,
		StartedAfter: state.Generation, Action: action, TargetURL: state.LastObservedURL,
		SourceURL: state.LastObservedURL, Follows: state.ContinuationID, Uncontended: true}
	if action == "open" {
		r.TargetURL, _ = args["url"].(string)
	}
	// Browser steps share a page. Any intervening call breaks direct continuity;
	// any overlap prevents either completion from claiming exclusive page state.
	for k, other := range p.browser {
		for seq, pending := range other.Pending {
			r.Uncontended = false
			pending.Uncontended = false
			other.Pending[seq] = pending
		}
		other.ContinuationID, other.LastObservedURL = "", ""
		p.browser[k] = other
	}
	if !r.Uncontended {
		r.Follows = ""
	}
	state = p.browser[key]
	state.Sequence = r.Sequence
	if state.Pending == nil {
		state.Pending = make(map[int]planBrowserReceipt)
	}
	state.Pending[r.Sequence] = r
	p.browser[key] = state
	return planBrowserCall{key: key, receipt: r}, nil
}

func (p *planTracker) discardBrowserCall(call planBrowserCall) {
	if p == nil || call.key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, exists := p.browser[call.key]
	if !exists {
		return
	}
	if r, ok := state.Pending[call.receipt.Sequence]; ok && r.Uncontended {
		state.LastObservedURL, state.ContinuationID = r.SourceURL, r.Follows
	}
	delete(state.Pending, call.receipt.Sequence)
	p.browser[call.key] = state
}

func (p *planTracker) completeBrowserCall(call planBrowserCall, output string) string {
	if p == nil || call.key == "" {
		return ""
	}
	type browserResult struct {
		OK        bool   `json:"ok"`
		URL       string `json:"url"`
		PageID    string `json:"page_id"`
		PageEpoch int64  `json:"page_epoch"`
		Form      *struct {
			Completed int `json:"completed"`
		} `json:"form"`
		Snapshot *struct {
			Text     string            `json:"text"`
			Elements []json.RawMessage `json:"elements"`
		} `json:"snapshot"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	var result browserResult
	if json.Unmarshal([]byte(output), &result) != nil {
		result = browserResult{} // malformed output cannot prove non-dispatch
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.browser[call.key]
	r, pending := state.Pending[call.receipt.Sequence]
	if !pending || r.ID != call.receipt.ID {
		return "" // duplicate completion or a callback from before restoration
	}
	delete(state.Pending, r.Sequence)
	state.Generation++
	r.Completed = state.Generation
	r.URL, r.OK, r.PageID, r.PageEpoch = result.URL, result.OK, result.PageID, result.PageEpoch
	r.Observed = result.OK && result.Snapshot != nil && (strings.TrimSpace(result.Snapshot.Text) != "" || len(result.Snapshot.Elements) > 0) && knownBrowserURL(result.URL)
	if result.Error != nil {
		r.Code = result.Error.Code
	}
	if !r.OK {
		if r.Code == "" {
			r.Code = "outcome_unknown"
		}
		if r.Action != "open" && knownBrowserURL(r.URL) {
			r.TargetURL = r.URL
		}
		// A field rejection is not proof that earlier fill_form fields were never
		// dispatched. Preserve partial/unknown mutations as verification obligations.
		partialForm := r.Action == "fill_form" && (result.Form == nil || result.Form.Completed != 0)
		r.ObservationOnly = browserObservationAction(r.Action) || browserPreDispatchFailure(r.Code, partialForm)
		addBrowserFailure(&state, r)
	}
	state.Observed = state.Observed || r.Observed
	if r.Uncontended {
		// A successful evaluate/file receipt need not carry a snapshot. Retain its
		// associated source without inventing a new observation or success proof.
		if knownBrowserURL(r.URL) {
			state.LastObservedURL = r.URL
		} else if r.OK || r.ObservationOnly {
			state.LastObservedURL = r.SourceURL
		}
		state.ContinuationID = r.ID
		if browserObservationAction(r.Action) && r.Follows != "" {
			state.ContinuationID = r.Follows
		}
	}
	state.Receipts = append(state.Receipts, r)
	if len(state.Receipts) > 32 {
		state.Receipts = state.Receipts[len(state.Receipts)-32:]
	}
	p.browser[call.key] = state
	data, _ := json.Marshal(struct {
		Step    string             `json:"step"`
		Receipt planBrowserReceipt `json:"receipt"`
	}{strings.TrimPrefix(call.key, "id:"), r})
	return "\n[Plan browser evidence] " + string(data) + "\nCite recovery receipt ids in update_plan.evidence_ids with the observed result in reason, including while other work remains active or blocked. Verified failures are cleared individually. Recovery must start after the failure completed. observation_only failures need a fresh valid observation, then finish the intended work. For uncertain mutations inspect with snapshot/wait before another action; direct same-page/epoch observations can verify a navigated result without replaying the mutation. Otherwise observe the associated required URL. Overlap or another source cannot prove recovery. Keep unverified work blocked with its concrete limitation; do not repeat done/continue merely to clear it."
}

func browserObservationAction(action string) bool { return action == "snapshot" || action == "wait" }

func browserPreDispatchFailure(code string, partialForm bool) bool {
	switch code {
	case "invalid_arguments", "unsupported_action", "identity_required", "invalid_identity":
		return true // whole-request rejection before entering the action engine
	case "stale_ref", "invalid_request", "not_ready", "ambiguous_selector", "unsupported_popup":
		return !partialForm // target validation; earlier form fields may have run
	default:
		return false // timeouts, cancellation, output limits and transport errors are uncertain
	}
}

func addBrowserFailure(state *planBrowserEvidence, r planBrowserReceipt) {
	if state.Failures == nil {
		state.Failures = make(map[string]planBrowserReceipt)
	}
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%t\x00%s\x00%d", r.Action, r.TargetURL, r.Code, r.ObservationOnly, r.PageID, r.PageEpoch)
	if previous, ok := state.Failures[key]; ok {
		r.Completed = max(previous.Completed, r.Completed)
	}
	if previous, ok := state.Failures[key]; ok && previous.Sequence > r.Sequence {
		// Keep the newest invocation identity, but a late older failure advances
		// the causal frontier too. Neither overlapping observation can resolve it.
		previous.Completed = max(previous.Completed, r.Completed)
		state.Failures[key] = previous
	} else {
		state.Failures[key] = r
	}
}

func (p *planTracker) validateStepLocked(step messages.PlanStep) messages.PlanStep {
	key := planStepKey(step)
	state, exists := p.browser[key]
	if !exists {
		return step
	}
	// Verification is incremental, even if done is rejected or the caller keeps
	// the step active/blocked. Resolved obligations survive receipt eviction.
	if strings.TrimSpace(step.Reason) != "" {
		for failureKey, failure := range state.Failures {
			for _, r := range state.Receipts {
				if slices.Contains(step.EvidenceIDs, r.ID) && browserRecoveryMatches(failure, r) {
					delete(state.Failures, failureKey)
					break
				}
			}
		}
	}
	p.browser[key] = state
	if step.Status != "done" {
		return step
	}
	fail := func(reason string) messages.PlanStep { step.Status = "blocked"; step.Reason = reason; return step }
	if len(state.Pending) > 0 {
		return fail("本步骤的浏览器操作仍在执行，请等待回执后核实。")
	}
	if len(state.Failures) > 0 {
		return fail("本步骤仍有未核实的浏览器操作。前置检查失败后获取有效页面观察；不确定操作先用 snapshot/wait 核实同一页面的结果，或核实关联原地址。用 evidence_ids 逐项引用回执并在 reason 说明结果；无法核实时说明限制，不要重复提交 done。")
	}
	if !state.Observed {
		return fail("尚无本步骤的成功页面观察；请完成实际操作，或保留受阻状态并说明限制。")
	}
	return step
}

func browserRecoveryMatches(failure, observed planBrowserReceipt) bool {
	if !observed.Observed || failure.Completed == 0 || observed.StartedAfter < failure.Completed {
		return false
	}
	if failure.ObservationOnly {
		return true
	}
	// A click may have navigated before its acknowledgement/observation failed.
	// Only a direct, non-overlapping read on the same browser page and GUI epoch
	// can verify that new URL. An intervening open (even in another step) cannot.
	if failure.Action != "open" && browserObservationAction(observed.Action) && observed.Uncontended &&
		observed.Follows == failure.ID && failure.PageID != "" && failure.PageEpoch > 0 &&
		observed.PageID == failure.PageID && observed.PageEpoch == failure.PageEpoch {
		return true
	}
	return sameObservedURL(failure.TargetURL, observed.URL) ||
		(observed.Action == "open" && sameObservedURL(failure.TargetURL, observed.TargetURL))
}
func knownBrowserURL(raw string) bool { return strings.TrimSpace(raw) != "" && raw != "about:blank" }
func sameObservedURL(want, got string) bool {
	if !knownBrowserURL(want) || !knownBrowserURL(got) {
		return false
	}
	a, e1 := url.Parse(want)
	b, e2 := url.Parse(got)
	// Browsers serialize an HTTP origin's empty path as "/". Other paths,
	// query values and fragments still describe different destinations.
	if e1 == nil && a.Path == "" {
		a.Path = "/"
	}
	if e2 == nil && b.Path == "" {
		b.Path = "/"
	}
	return e1 == nil && e2 == nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && a.EscapedPath() == b.EscapedPath() && a.Query().Encode() == b.Query().Encode() && a.Fragment == b.Fragment
}

// Explain which already-delivered observations can resolve each obligation.
// Keep this model-facing, so the UI need not expose receipt bookkeeping.
type browserRecoveryNeed struct {
	Step       string   `json:"step"`
	Failure    string   `json:"failure"`
	Candidates []string `json:"available_evidence_ids"`
}

func (p *planTracker) browserRecoveryNeeds() []browserRecoveryNeed {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var needs []browserRecoveryNeed
	for _, step := range p.steps {
		state := p.browser[planStepKey(step)]
		var failures []planBrowserReceipt
		for _, failure := range state.Failures {
			failures = append(failures, failure)
		}
		slices.SortFunc(failures, func(a, b planBrowserReceipt) int { return a.Sequence - b.Sequence })
		for _, failure := range failures {
			need := browserRecoveryNeed{Step: step.ID, Failure: failure.ID, Candidates: []string{}}
			for _, receipt := range state.Receipts {
				if browserRecoveryMatches(failure, receipt) {
					need.Candidates = append(need.Candidates, receipt.ID)
				}
			}
			needs = append(needs, need)
		}
	}
	return needs
}

func cloneBrowserEvidence(src map[string]planBrowserEvidence) map[string]planBrowserEvidence {
	if src == nil {
		return nil
	}
	out := make(map[string]planBrowserEvidence, len(src))
	for k, state := range src {
		state.Receipts = slices.Clone(state.Receipts)
		state.Pending = cloneBrowserMap(state.Pending)
		state.Failures = cloneBrowserMap(state.Failures)
		out[k] = state
	}
	return out
}
func cloneBrowserMap[K comparable](src map[K]planBrowserReceipt) map[K]planBrowserReceipt {
	if src == nil {
		return nil
	}
	out := make(map[K]planBrowserReceipt, len(src))
	for k, r := range src {
		out[k] = r
	}
	return out
}

// Normalize older journals without treating their invocation sequence as causal
// evidence. Interrupted calls become new unknown failures, never eternal Pending.
func restoreBrowserState(state planBrowserEvidence) planBrowserEvidence {
	for _, r := range state.Receipts {
		state.Generation = max(state.Generation, r.Completed, r.StartedAfter)
		state.Sequence = max(state.Sequence, r.Sequence)
		state.Observed = state.Observed || r.Observed
	}
	for _, r := range state.Failures {
		state.Generation = max(state.Generation, r.Completed)
		state.Sequence = max(state.Sequence, r.Sequence)
	}
	failures := state.Failures
	state.Failures = nil
	for _, r := range failures {
		if r.Completed == 0 {
			state.Generation++
			r.Completed = state.Generation
		}
		if r.Action != "open" && !knownBrowserURL(r.TargetURL) && knownBrowserURL(r.URL) {
			r.TargetURL = r.URL
		}
		// Old receipts omitted form progress, so target-validation errors from a
		// legacy fill_form remain uncertain. Single-action preflight errors are safe.
		r.ObservationOnly = r.ObservationOnly || browserObservationAction(r.Action) || browserPreDispatchFailure(r.Code, r.Action == "fill_form")
		addBrowserFailure(&state, r)
	}
	for _, r := range state.Pending {
		state.Generation++
		state.Sequence = max(state.Sequence, r.Sequence)
		r.Completed, r.Code = state.Generation, "outcome_unknown"
		addBrowserFailure(&state, r)
		state.LastObservedURL = ""
		state.ContinuationID = ""
	}
	state.Pending = nil
	return state
}
