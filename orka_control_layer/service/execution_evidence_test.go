package service

import (
	"context"
	"strings"
	"testing"
)

func TestExecutionEvidencePreservesFailureAndDoesNotClaimAcceptance(t *testing.T) {
	scope := acceptanceScope{base: t.TempDir(), owner: "tester", conversation: "conv", runID: "run"}
	ctx := context.WithValue(context.Background(), acceptanceScopeKey{}, scope)
	for _, receipt := range []string{
		`tool "shell" error: {"ok":false,"exit_code":7,"stdout":"failed","stderr":"trace","timed_out":false,"canceled":false}`,
		`{"ok":true,"exit_code":0,"stdout":"FAILED is text","stderr":"","timed_out":false,"canceled":false}`,
	} {
		if note := recordExecution(ctx, "shell", map[string]any{"command": "run-tests"}, receipt); !strings.Contains(note, "process execution only") {
			t.Fatal(note)
		}
	}
	records, partial, err := readExecutionEvidence(scope)
	if err != nil || partial || len(records) != 2 {
		t.Fatalf("%v %v %+v", err, partial, records)
	}
	if records[0].OK || records[0].ExitCode != 7 || records[0].Stderr != "trace" || !records[1].OK {
		t.Fatalf("%+v", records)
	}
	history, err := readAcceptance(scope)
	if err != nil || len(history.Checks) != 0 {
		t.Fatal("process output became business acceptance", history, err)
	}
	other := scope
	other.owner = "other"
	if leaked, _, err := readExecutionEvidence(other); err != nil || len(leaked) != 0 {
		t.Fatal("cross-owner evidence", leaked, err)
	}
}

func TestExecutionEvidenceRejectsMissingOutcomeAndOtherTools(t *testing.T) {
	ctx := context.WithValue(context.Background(), acceptanceScopeKey{}, acceptanceScope{base: t.TempDir(), owner: "a", conversation: "b", runID: "c"})
	for _, text := range []string{`{"ok":true}`, `{"ok":true,"exit_code":0}`, `permission denied`, `{"stdout":"OK"}`} {
		if note := recordExecution(ctx, "shell", nil, text); note != "" {
			t.Fatal(note)
		}
	}
	if note := recordExecution(ctx, "file_read", nil, `{"ok":true,"exit_code":0,"stdout":"","stderr":""}`); note != "" {
		t.Fatal(note)
	}
}

func TestExecutionEvidenceRetainsRefusalReason(t *testing.T) {
	scope := acceptanceScope{base: t.TempDir(), owner: "a", conversation: "b", runID: "c"}
	ctx := context.WithValue(context.Background(), acceptanceScopeKey{}, scope)
	recordExecution(ctx, "shell", nil, `{"ok":false,"exit_code":-1,"stdout":"","stderr":"","error":"command refused"}`)
	records, _, err := readExecutionEvidence(scope)
	if err != nil || len(records) != 1 || records[0].Error != "command refused" {
		t.Fatalf("%+v %v", records, err)
	}
}

func TestEmptyFailedProcessExplainsDiagnosticRecovery(t *testing.T) {
	ctx := withAcceptance(context.Background(), t.TempDir(), "owner", "conversation", "empty-failure")
	failed := `{"ok":false,"exit_code":1,"stdout":"","stderr":"","error":"exit status 1"}`
	got := recordExecution(ctx, "shell", map[string]any{"command": "tests > /tmp/run.log; cat /tmp/run.log"}, failed)
	for _, want := range []string{"exit_code=1", "without captured stdout/stderr", "workspace-relative log", "errexit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	for _, receipt := range []string{
		`{"ok":true,"exit_code":0,"stdout":"","stderr":""}`,
		`{"ok":false,"exit_code":1,"stdout":"","stderr":"specific failure"}`,
		`{"ok":false,"exit_code":-1,"stdout":"","stderr":"","error":"command refused"}`,
		`{"ok":false,"exit_code":1,"stdout":"","stderr":"","timed_out":true}`,
	} {
		if note := recordExecution(ctx, "shell", nil, receipt); strings.Contains(note, "without captured stdout/stderr") {
			t.Fatal("inapplicable recovery hint", note)
		}
	}
}
