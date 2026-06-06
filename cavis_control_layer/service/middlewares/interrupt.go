package middlewares

import "github.com/cavis-oss/cavis_core/agent"

// Interrupt promotes a declarative clarify request (Vars[VarPendingClarify])
// into a runner interrupt. It is a no-op if an interrupt was already set
// upstream (e.g. by tools-mid) or if the run is being resumed.
//
// In the ADK pipeline clarify is normally raised by tools-mid (which keeps the
// cursor on itself for correct resume); this middleware exists as a reusable
// interrupt finalizer for non-loop stages that want to request clarification.
type Interrupt struct{}

func (m *Interrupt) Name() string { return "interrupt-mid" }

func (m *Interrupt) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	if rc.Interrupt != nil {
		return nil
	}
	if isResumed(rc) {
		return nil
	}
	if clar, ok := getPendingClarify(rc); ok {
		c := clar
		rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &c}
	}
	return nil
}
