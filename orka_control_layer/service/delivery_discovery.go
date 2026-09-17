package service

import "github.com/orka-oss/orka_core/artifacts"

// Actual writes are an opportunity to remind the agent about delivery intent,
// not permission to turn every temporary file into a contractual requirement.
// At most one reminder per run; repeated reminders would inflate long contexts.
func (d *deliveryTracker) noteUndeclaredWrites(paths []string) string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.outputs) > 0 || d.missingOutputsNotified {
		return ""
	}
	for _, p := range paths {
		if !artifacts.ValidPath(p) {
			continue
		}
		d.missingOutputsNotified = true
		return "\n[Workspace files changed, but no delivery outputs are registered. If the user requested files, register only the intended final deliverables in update_plan.outputs so final delivery checks and snapshots include them. Temporary files are not automatically deliverables. For prose-only tasks, do not invent file deliverables.]"
	}
	return ""
}
