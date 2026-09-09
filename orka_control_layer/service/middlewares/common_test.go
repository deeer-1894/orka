package middlewares

import (
	"strings"
	"testing"
)

// The asymmetry this closes: research tasks were told three times over where to
// put what they learn ("write each finding down AS YOU GET IT"), and tasks that
// BUILD something were told once, vaguely. A model with nowhere to put a draft
// put it in its reasoning — never saved, not runnable, truncated at 82,903
// characters with nothing written and the run filed as done.
func TestSystemPromptTellsTheModelWhereToPutAnArtifact(t *testing.T) {
	for _, want := range []string{"ARTIFACT", "WORKING", "file_write", "Never draft file contents"} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Errorf("the prompt no longer says %q, so a build task has no externalisation route", want)
		}
	}
}

// The research guidance is what the build guidance was modelled on; losing
// either re-opens the failure the other one covers.
func TestSystemPromptKeepsTheResearchGuidance(t *testing.T) {
	for _, want := range []string{"notes.md", "actually retrieve", "cite the source"} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Errorf("the prompt no longer says %q", want)
		}
	}
}
