package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/acceptance"
	"github.com/orka-oss/orka_core/messages"
)

type acceptanceScope struct {
	base, owner, conversation, runID string
	inherited                        []string
}
type acceptanceScopeKey struct{}
type AcceptanceContract struct {
	InheritedRunIDs []string `json:"inherited_run_ids"`
	RunID           string   `json:"run_id"`
	Requests        []string `json:"requests"`
	Truncated       bool     `json:"truncated"`
}
type AcceptanceRecord struct {
	RunID    string            `json:"run_id"`
	At       int64             `json:"at"`
	SpecPath string            `json:"spec_path"`
	Report   acceptance.Report `json:"report"`
}
type AcceptanceHistory struct {
	Contract AcceptanceContract `json:"contract"`
	Checks   []AcceptanceRecord `json:"checks"`
}

func acceptanceDir(scope acceptanceScope) (string, error) {
	if scope.base == "" || scope.owner == "" || scope.conversation == "" || scope.runID == "" {
		return "", errors.New("acceptance storage is not configured")
	}
	for _, id := range []string{scope.conversation, scope.runID} {
		if filepath.Base(id) != id || id == "." || id == ".." || strings.Contains(id, "\\") {
			return "", errors.New("invalid acceptance identity")
		}
	}
	sum := sha256.Sum256([]byte(scope.owner))
	return filepath.Join(scope.base, ".orka_acceptance", hex.EncodeToString(sum[:]), scope.conversation, scope.runID), nil
}
func withAcceptance(ctx context.Context, base, owner, conv, runID string) context.Context {
	scope := acceptanceScope{base: base, owner: owner, conversation: conv, runID: runID}
	if resumed := runResumeFrom(ctx); resumed != nil && resumed.Checkpoint != nil {
		scope.inherited = append([]string(nil), resumed.Checkpoint.AcceptanceRunIDs...)
	}
	return context.WithValue(ctx, acceptanceScopeKey{}, scope)
}

// persistAcceptanceContract archives actual human inputs independently of the
// model's mutable checklist. Storage is outside executable session workspaces.
func persistAcceptanceContract(ctx context.Context, requests []string) error {
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	dir, err := acceptanceDir(scope)
	if err != nil {
		return err
	}
	history, err := readAcceptance(scope)
	if err != nil {
		return err
	}
	contract := AcceptanceContract{RunID: scope.runID, Requests: []string{}, InheritedRunIDs: history.Contract.InheritedRunIDs}
	remaining := 128 << 10
	for _, request := range requests {
		if len(request) > remaining {
			request = request[:remaining]
			contract.Truncated = true
		}
		contract.Requests = append(contract.Requests, request)
		remaining -= len(request)
		if remaining == 0 {
			break
		}
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	body, err := json.Marshal(contract)
	if err != nil {
		return err
	}
	return writeNewAcceptance(filepath.Join(dir, "contract.json"), body)
}
func writeNewAcceptance(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(body)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// Append-only human amendments leave the original contract intact. These files
// also retain accepted messages if the process stops before the next model call.
func persistSteeringRequest(ctx context.Context, m messages.Message) error {
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if scope.base == "" {
		return nil
	}
	dir, err := acceptanceDir(scope)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeNewAcceptance(filepath.Join(dir, fmt.Sprintf("steering-%019d-%s.json", time.Now().UnixNano(), m.ID)), body)
}

func loadSteeringRequests(ctx context.Context) ([]messages.Message, error) {
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if scope.base == "" || scope.owner == "" || scope.conversation == "" || scope.runID == "" {
		return nil, nil
	}
	var out []messages.Message
	for _, id := range append(append([]string(nil), scope.inherited...), scope.runID) {
		scope.runID = id
		dir, err := acceptanceDir(scope)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "steering-") || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			if info.Size() > 1<<20 {
				return nil, errors.New("steering record exceeds limit")
			}
			body, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			var m messages.Message
			if err := json.Unmarshal(body, &m); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
	}
	return out, nil
}
func saveAcceptanceCheck(ctx context.Context, record AcceptanceRecord) error {
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	dir, err := acceptanceDir(scope)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	record.RunID = scope.runID
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	name := fmt.Sprintf("%019d-%x.json", time.Now().UnixNano(), sum[:8])
	return writeNewAcceptance(filepath.Join(dir, name), body)
}
func (s *ChatService) AcceptanceFor(owner, conv, runID string) (AcceptanceHistory, error) {
	return readAcceptance(acceptanceScope{base: s.Cfg.Storage.BaseStoragePath, owner: owner, conversation: conv, runID: runID})
}
func readAcceptanceRun(scope acceptanceScope) (AcceptanceHistory, error) {
	runID := scope.runID
	dir, err := acceptanceDir(scope)
	if err != nil {
		return AcceptanceHistory{}, err
	}
	history := AcceptanceHistory{Contract: AcceptanceContract{RunID: runID, Requests: []string{}}, Checks: []AcceptanceRecord{}}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return history, nil
	}
	if err != nil {
		return history, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return history, err
		}
		if info.Size() > 8<<20 {
			return history, errors.New("acceptance record exceeds limit")
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return history, err
		}
		if entry.Name() == "contract.json" {
			if err = json.Unmarshal(body, &history.Contract); err != nil {
				return history, err
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), "steering-") {
			var m messages.Message
			if err = json.Unmarshal(body, &m); err != nil {
				return history, err
			}
			history.Contract.Requests = append(history.Contract.Requests, m.Content)
			continue
		}
		var record AcceptanceRecord
		if err = json.Unmarshal(body, &record); err != nil {
			return history, err
		}
		history.Checks = append(history.Checks, record)
		if len(history.Checks) > 128 {
			return history, errors.New("too many acceptance records; inspect archive")
		}
	}
	return history, nil
}

type acceptanceCheckTool struct{}

func (acceptanceCheckTool) Name() string { return "check_acceptance" }
func (acceptanceCheckTool) Description() string {
	return `Verify explicit requirements from a *.acceptance.json file: {"kind":"orka.acceptance/v1","requirements":[{"id":"margin","description":"minimum gross margin","method":"csv","file":"pricing.csv","column":"margin","operation":"min","compare":"gte","expected":"0.65"}]}. Methods: contains (file/expected text), csv (count rows WITHOUT column; count_nonempty or count_rfc3339 require column and count valid cells; value compares exactly one filtered numeric cell; min/max/sum aggregate numeric cells; eq/gte/lte; exact where or exclude filters), manual (remains unverified). Checks use real files and exact rational arithmetic. Add requirements for changed constraints; never replace business validation with existence checks. Declared assertions do not prove completeness of original requests or source support. The spec becomes a required output; immutable audit records retain every check.`
}
func (acceptanceCheckTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}
}
func (acceptanceCheckTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if !strings.HasSuffix(path, ".acceptance.json") {
		return "", errors.New("expected *.acceptance.json")
	}
	d := deliveryFrom(ctx)
	if d == nil {
		return "", errors.New("workspace unavailable")
	}
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	spec, err := acceptance.ReadSpec(root.FS(), path)
	if err != nil {
		return "", err
	}
	if err = acceptance.Validate(spec); err != nil {
		return "", err
	}
	if err = d.declare([]string{path}); err != nil {
		return "", err
	}
	report := checkAcceptanceHistory(ctx, root.FS(), path, spec)
	record := AcceptanceRecord{At: time.Now().UnixMilli(), SpecPath: path, Report: report}
	if err = saveAcceptanceCheck(ctx, record); err != nil {
		return "", err
	}
	body, err := json.Marshal(report)
	return string(body), err
}

// Previously observed IDs cannot disappear from a revised spec. Changed
// assertions produce a new immutable audit record, preserving both versions.
func checkAcceptanceHistory(ctx context.Context, files fs.FS, path string, spec acceptance.Spec) acceptance.Report {
	report := acceptance.Check(ctx, files, spec)
	scope, _ := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	history, err := readAcceptance(scope)
	if err != nil {
		report.OK = false
		report.Error = "acceptance history unavailable"
		return report
	}
	current := map[string]bool{}
	for _, r := range spec.Requirements {
		current[r.ID] = true
	}
	missing := map[string]bool{}
	for _, check := range history.Checks {
		if check.SpecPath != path {
			continue
		}
		for _, prior := range check.Report.Results {
			if !current[prior.ID] && !missing[prior.ID] {
				report.OK = false
				missing[prior.ID] = true
				report.Results = append(report.Results, acceptance.Result{Requirement: prior.Requirement, Status: "failed", Detail: "Previously declared requirement was removed. Restore this exact id in the same spec and update its assertion there; renaming/deleting the spec or id does not remove the original obligation. This is persisted requirement history, not a stale cache."})
			}
		}
	}
	return report
}
func acceptanceFailures(ctx context.Context) []string {
	scope, ok := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if !ok || scope.runID == "" {
		return nil
	}
	if _, err := readAcceptance(scope); err != nil {
		return []string{"验收继承记录不可读取：" + err.Error()}
	}
	d := deliveryFrom(ctx)
	if d == nil {
		return nil
	}
	root, err := os.OpenRoot(d.root)
	if err != nil {
		return []string{"验收工作区不可读取"}
	}
	defer root.Close()
	var failures []string
	for _, path := range d.snapshot() {
		if !strings.HasSuffix(path, ".acceptance.json") {
			continue
		}
		spec, err := acceptance.ReadSpec(root.FS(), path)
		if err != nil {
			failures = append(failures, "验收清单不可读取："+path)
			continue
		}
		report := checkAcceptanceHistory(ctx, root.FS(), path, spec)
		if err := saveAcceptanceCheck(ctx, AcceptanceRecord{At: time.Now().UnixMilli(), SpecPath: path, Report: report}); err != nil {
			failures = append(failures, "验收记录保存失败："+path)
		}
		if !report.OK {
			failures = append(failures, "业务验收未全部通过："+path)
		}
	}
	return failures
}
