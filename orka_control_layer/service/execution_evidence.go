package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/messages"
)

// ExecutionEvidence records a process observation outside the mutable workspace.
// A successful exit is deliberately NOT converted to a passed business/UI check.
type ExecutionEvidence struct {
	RevisionScope    string              `json:"revision_scope,omitempty"`
	Files            []ExecutionRevision `json:"files,omitempty"`
	ChangedDuring    []string            `json:"changed_during,omitempty"`
	ChangedSince     []string            `json:"changed_since,omitempty"`
	RevisionsPartial bool                `json:"revisions_partial,omitempty"`
	ID               string              `json:"id"`
	RunID            string              `json:"run_id"`
	At               int64               `json:"at"`
	Tool             string              `json:"tool"`
	Command          string              `json:"command"`
	OK               bool                `json:"ok"`
	ExitCode         int                 `json:"exit_code"`
	TimedOut         bool                `json:"timed_out"`
	Canceled         bool                `json:"canceled"`
	Stdout           string              `json:"stdout"`
	Stderr           string              `json:"stderr"`
	Error            string              `json:"error,omitempty"`
	OutputTruncated  bool                `json:"output_truncated"`
}

// MCP errors prefix their JSON receipt with `tool "name" error: `. Remove only
// that exact envelope; never search arbitrary stdout for a success/failure word.
func executionReceipt(name, text string) string {
	return strings.TrimPrefix(text, fmt.Sprintf("tool %q error: ", name))
}

func recordExecution(ctx context.Context, name string, args map[string]any, text string, snapshots ...executionRevisionSnapshot) string {
	if name != "shell" && name != "python" {
		return ""
	}
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if scope.runID == "" {
		return ""
	}
	var raw struct {
		OK          *bool   `json:"ok"`
		ExitCode    *int    `json:"exit_code"`
		Stdout      *string `json:"stdout"`
		Stderr      *string `json:"stderr"`
		TimedOut    bool    `json:"timed_out"`
		Canceled    bool    `json:"canceled"`
		Error       string  `json:"error"`
		FileChanges struct {
			Paths []string `json:"paths"`
		} `json:"file_changes"`
	}
	if json.Unmarshal([]byte(executionReceipt(name, text)), &raw) != nil || raw.OK == nil || raw.ExitCode == nil || raw.Stdout == nil || raw.Stderr == nil {
		return ""
	}
	dir, err := acceptanceDir(scope)
	if err != nil {
		return "\n[Execution evidence could not be saved.]"
	}
	command, _ := args["command"].(string)
	if name == "python" {
		command, _ = args["code"].(string)
		if command == "" {
			command = fmt.Sprint(args["path"], " ", args["argv"])
		}
	}
	record := ExecutionEvidence{ID: messages.NewID(), RunID: scope.runID, At: time.Now().UnixMilli(), Tool: name,
		Command: trunc(command, 4096), OK: *raw.OK, ExitCode: *raw.ExitCode, TimedOut: raw.TimedOut, Canceled: raw.Canceled,
		Stdout: trunc(*raw.Stdout, 4096), Stderr: trunc(*raw.Stderr, 4096),
		Error:           trunc(raw.Error, 4096),
		OutputTruncated: len([]rune(*raw.Stdout)) > 4096 || len([]rune(*raw.Stderr)) > 4096}
	if len(snapshots) > 0 {
		before := snapshots[0]
		var paths []string
		for _, file := range before.files {
			paths = append(paths, file.Path)
		}
		var after []ExecutionRevision
		partial := false
		if d := deliveryFrom(ctx); d != nil && len(paths) > 0 {
			after, partial = hashExecutionPaths(ctx, d.root, paths)
		}
		record.RevisionScope = before.scope
		record.Files = before.files
		record.ChangedDuring = changedRevisions(before.files, after)
		record.RevisionsPartial = before.partial || partial
	}
	body, _ := json.Marshal(record)
	dir = filepath.Join(dir, "executions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "\n[Execution evidence could not be saved.]"
	}
	if err := writeNewAcceptance(filepath.Join(dir, fmt.Sprintf("%019d-%s.json", time.Now().UnixNano(), record.ID)), body); err != nil {
		return "\n[Execution evidence could not be saved.]"
	}
	return fmt.Sprintf("\n[Execution evidence %s: exit_code=%d. This records process execution only; it does not verify UI interaction or completeness of requirements.]", record.ID, record.ExitCode) +
		executionFailureHint(record) +
		deliveryFrom(ctx).noteUndeclaredWrites(raw.FileChanges.Paths) +
		deliveryFrom(ctx).inspectWrittenArchives(ctx, raw.FileChanges.Paths)
}

// Empty failure receipts provide no diagnostic for the model to act on. Guide
// recovery without guessing a cause, changing the exit status, or retrying it.
func executionFailureHint(record ExecutionEvidence) string {
	if record.OK || record.ExitCode <= 0 || record.TimedOut || record.Canceled || strings.TrimSpace(record.Stdout+record.Stderr) != "" {
		return ""
	}
	return "\n[The process failed without captured stdout/stderr. Read any existing saved failure log before rerunning. If another execution is necessary, capture its output to a workspace-relative log and handle its exit status explicitly (Python subprocess.run(check=False), or shell if command; then ...; else ...; fi). Shell errexit stops later echo/cat commands after an unhandled failure; /tmp does not survive a later tool call. Do not infer the cause or claim tests passed from this empty receipt.]"
}

func readExecutionEvidence(scope acceptanceScope) ([]ExecutionEvidence, bool, error) {
	return readExecutionEvidenceLimit(scope, 200)
}
func readExecutionEvidenceLimit(scope acceptanceScope, limit int) ([]ExecutionEvidence, bool, error) {
	dir, err := acceptanceDir(scope)
	if err != nil {
		return nil, false, err
	}
	dir = filepath.Join(dir, "executions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []ExecutionEvidence{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	partial := len(entries) > limit
	if partial {
		entries = entries[len(entries)-limit:]
	}
	out := []ExecutionEvidence{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, partial, err
		}
		if info.Size() > 128<<10 {
			return nil, partial, fmt.Errorf("execution record exceeds limit")
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, partial, err
		}
		var record ExecutionEvidence
		if err := json.Unmarshal(body, &record); err != nil {
			return nil, partial, err
		}
		if record.RunID != scope.runID {
			return nil, partial, fmt.Errorf("execution record identity mismatch")
		}
		out = append(out, record)
	}
	return out, partial, nil
}

type executionRevisionSnapshot struct {
	scope   string
	files   []ExecutionRevision
	partial bool
}
