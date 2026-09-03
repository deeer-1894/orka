package service

import (
	"strings"
	"testing"
)

// A delegate's transcript is discarded when its agent tool returns, so whatever
// it did not write down is gone. The researcher reads roughly 143k tokens per
// delegation and returns 1-7KB; without file_write it physically cannot record
// the difference, and a later question about a detail it saw costs a second
// delegation that re-fetches the same sources.
func TestResearcherCanRecordWhatItReads(t *testing.T) {
	var researcher *struct{ tools []string }
	for _, sp := range DefaultSubAgents() {
		if sp.Name == "researcher" {
			researcher = &struct{ tools []string }{sp.Tools}
		}
	}
	if researcher == nil {
		t.Fatal("no researcher in the default registry")
	}
	has := map[string]bool{}
	for _, n := range researcher.tools {
		has[n] = true
	}
	for _, need := range []string{"file_write", "file_read"} {
		if !has[need] {
			t.Errorf("researcher lacks %s, so its findings cannot outlive the delegation", need)
		}
	}
	// It still has to be able to gather, or it has nothing to record.
	for _, need := range []string{"web_search", "fetch_url"} {
		if !has[need] {
			t.Errorf("researcher lost %s", need)
		}
	}
}

// The point is a POINTER back, not a bigger reply: if the delegate both writes
// the file and repeats it in the return value, the orchestrator's context grows
// exactly as before and nothing was gained.
func TestResearcherPromptAsksForAPointerNotTheText(t *testing.T) {
	p := researcherPrompt
	for _, want := range []string{"file_write", "findings/", "file_read"} {
		if !strings.Contains(p, want) {
			t.Errorf("researcher prompt never mentions %q", want)
		}
	}
	if !strings.Contains(p, "Do NOT repeat the full findings") {
		t.Error("prompt does not stop the delegate from returning the findings inline")
	}
}

// Scoped tools are resolved by name against the run's tool set, so a delegate
// asking for a tool nobody provides is silently dropped from the registry
// (BuildEinoSubAgentTools skips a spec whose tools all miss). Keep the names in
// step with what the file tools are actually called.
func TestSubAgentToolNamesAreReal(t *testing.T) {
	known := map[string]bool{
		"web_search": true, "fetch_url": true, "http_request": true, "current_time": true,
		"file_write": true, "file_read": true, "file_list": true, "shell": true,
		"run_agent": true, "pdf_extract": true, "validate_factor": true,
		"recall_similar_factors": true, "sql_query": true,
	}
	for _, sp := range DefaultSubAgents() {
		for _, n := range sp.Tools {
			if !known[n] {
				t.Errorf("sub-agent %q scopes unknown tool %q", sp.Name, n)
			}
		}
	}
}
