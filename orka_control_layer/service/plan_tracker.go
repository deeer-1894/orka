package service

import (
	"slices"
	"sync"

	"github.com/orka-oss/orka_core/messages"
)

// planTracker preserves every step published via update_plan, so
// omitted steps cannot erase obligations. File requirements are checked separately.
// Without a tracker "done" means
// only "the model stopped calling tools", which is not a completion signal at
// all — it is equally true of a finished run and an abandoned one.
type planTracker struct {
	mu      sync.Mutex
	steps   []messages.PlanStep
	browser map[string]planBrowserEvidence
}

func (p *planTracker) record(steps []messages.PlanStep) {
	if p == nil {
		return
	}
	p.mu.Lock()
	// Omission is not completion. Explicit IDs let the model rename a step
	// without creating a second obligation; legacy title-only updates retain
	// their exact-title behavior for backward compatibility.
	index := make(map[string]int, len(p.steps))
	for i, step := range p.steps {
		index[planStepKey(step)] = i
	}
	for _, step := range clonePlanSteps(steps) {
		if i, ok := index[planStepKey(step)]; ok {
			step = p.validateStepLocked(step)
			p.steps[i] = step
		} else {
			step = p.validateStepLocked(step)
			index[planStepKey(step)] = len(p.steps)
			p.steps = append(p.steps, step)
		}
	}
	p.mu.Unlock()
}

func planStepKey(step messages.PlanStep) string {
	if step.ID != "" {
		return "id:" + step.ID
	}
	return "title:" + step.Title
}

func (p *planTracker) completed() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.steps) == 0 {
		return false
	}
	for _, step := range p.steps {
		if step.Status != "done" {
			return false
		}
	}
	return true
}

func (p *planTracker) snapshot() []messages.PlanStep {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePlanSteps(p.steps)
}

// same reports whether steps are identical to the plan already recorded, so a
// re-post of an unchanged checklist can be answered without spending an event.
// An empty tracker is never "the same": the first plan of a run is always news.
func (p *planTracker) same(steps []messages.PlanStep) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.steps) == 0 || len(p.steps) != len(steps) {
		return false
	}
	for i := range steps {
		if planStepKey(steps[i]) != planStepKey(p.steps[i]) || steps[i].Title != p.steps[i].Title || steps[i].Status != p.steps[i].Status || steps[i].Reason != p.steps[i].Reason || !slices.Equal(steps[i].EvidenceIDs, p.steps[i].EvidenceIDs) {
			return false
		}
	}
	return true
}

// unfinished returns the titles of steps still pending, active, or blocked. A plan the
// agent never published yields nothing, which correctly means "no claim to
// check" rather than "incomplete".
func (p *planTracker) unfinished() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, s := range p.steps {
		if s.Status != "done" {
			out = append(out, s.Title)
		}
	}
	return out
}

func clonePlanSteps(steps []messages.PlanStep) []messages.PlanStep {
	out := append([]messages.PlanStep(nil), steps...)
	for i := range out {
		out[i].EvidenceIDs = slices.Clone(out[i].EvidenceIDs)
	}
	return out
}

// One tracker snapshot carries the plan and its evidence from the same instant.
// The run journal owns serialization; it never locks or reconstructs tracker state.
type planCheckpoint struct {
	Steps   []messages.PlanStep
	Browser map[string]planBrowserEvidence
}

func (p *planTracker) checkpoint() planCheckpoint {
	if p == nil {
		return planCheckpoint{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return planCheckpoint{Steps: clonePlanSteps(p.steps), Browser: cloneBrowserEvidence(p.browser)}
}
func (p *planTracker) restore(saved planCheckpoint, legacyFailed bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps, p.browser = clonePlanSteps(saved.Steps), cloneBrowserEvidence(saved.Browser)
	for key, state := range p.browser {
		p.browser[key] = restoreBrowserState(state)
	}
	for i := range p.steps {
		step := &p.steps[i]
		key := planStepKey(*step)
		state := p.browser[key]
		if len(state.Failures) > 0 && step.Status == "done" {
			// Old non-atomic snapshots can pair done with newer unresolved evidence.
			step.Status, step.Reason = "blocked", "恢复的浏览器回执仍有未核实操作，请先核实原页面。"
		}
		if legacyFailed && len(saved.Browser) == 0 && step.Status != "done" {
			if p.browser == nil {
				p.browser = make(map[string]planBrowserEvidence)
			}
			p.browser[key] = planBrowserEvidence{}
			step.Status, step.Reason = "blocked", "旧运行的浏览器错误缺少步骤关联，请重新核实该步骤。"
		}
	}
}
