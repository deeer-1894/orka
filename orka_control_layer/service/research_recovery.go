package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/orka-oss/orka_core/pathsafe"
)

// Metadata and exact-byte references live in the trusted run checkpoint, never
// the mutable workspace catalog. Source bodies remain only in session files.
type evidenceReference struct {
	evidenceRecord
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type evidenceCheckpoint struct {
	Version  int                 `json:"version"`
	Scope    string              `json:"scope"`
	Records  []evidenceReference `json:"records"`
	Warnings []string            `json:"recovery_warnings,omitempty"`
}

// A reference can only be loaded by the same session backend that captured it.
// Hash the root rather than exposing host paths or accepting paths from the
// checkpoint as a new root. Unsupported/non-session backends fail closed.
func (s *evidenceStore) recoveryScope() string {
	backend, ok := s.backend.(*workspaceBackend)
	if !ok || backend == nil || backend.root == "" {
		return ""
	}
	root, err := filepath.Abs(backend.root)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(hash[:])
}
func (s *evidenceStore) checkpoint() *evidenceCheckpoint {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &evidenceCheckpoint{Version: 1, Scope: s.recoveryScope(), Records: []evidenceReference{}, Warnings: append([]string(nil), s.recoveryWarnings...)}
	for _, r := range s.records {
		r.body, r.Excerpt = "", ""
		c.Records = append(c.Records, evidenceReference{evidenceRecord: r, SHA256: hex.EncodeToString(r.persistedHash[:]), Bytes: r.persistedBytes})
	}
	return c
}
func (s *researchSession) restoreCheckpoint(ctx context.Context, c *runCheckpoint) {
	if c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = max(s.calls, c.ResearchCalls)
	s.evidence.restore(ctx, c.Evidence, c.ResearchCalls)
}
func (s *evidenceStore) recoveryProblems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recoveryWarnings...)
}
func (s *evidenceStore) recoveryProblemLocked(message string) {
	for _, previous := range s.recoveryWarnings {
		if previous == message {
			return
		}
	}
	s.recoveryWarnings = append(s.recoveryWarnings, message)
}

// Each record is checked independently: one missing page must not erase valid
// sources. No directory scan, fresh fetch, new retrieval date, or budget reset.
func (s *evidenceStore) restore(ctx context.Context, c *evidenceCheckpoint, previousCalls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == nil {
		if previousCalls > 0 {
			s.recoveryProblemLocked("No trusted evidence references in this legacy checkpoint; existing files were not scanned or promoted to verified sources. Retrieval allowance was not reset.")
		}
		return
	}
	for _, warning := range c.Warnings {
		s.recoveryProblemLocked(warning)
	}
	if c.Version != 1 || c.Scope == "" || c.Scope != s.recoveryScope() {
		s.recoveryProblemLocked("Evidence references have an unsupported version or a different/unavailable session scope; no source was restored.")
		return
	}
	backend := s.backend.(*workspaceBackend)
	checkedRoot, err := pathsafe.Resolve(backend.root, ".")
	if err != nil {
		s.recoveryProblemLocked("Session evidence workspace failed path validation; no source was restored.")
		return
	}
	root, err := os.OpenRoot(checkedRoot)
	if err != nil {
		s.recoveryProblemLocked("Session evidence workspace is unavailable; no source was restored.")
		return
	}
	defer root.Close()
	// Limit loading even for corrupt checkpoints and unexpectedly large files.
	const maxRecords = 512
	const maxBytes int64 = 64 << 20
	if len(c.Records) > maxRecords {
		s.recoveryProblemLocked("Evidence reference count exceeds recovery bounds; no source was restored.")
		return
	}
	remaining := maxBytes
	for _, ref := range c.Records {
		problem := func(reason string) {
			s.recoveryProblemLocked(fmt.Sprintf("Evidence %q (%q) not restored: %s. Do not cite it as the original capture.", ref.ID, ref.Path, reason))
		}
		if err := ctx.Err(); err != nil {
			s.recoveryProblemLocked("Evidence recovery interrupted; remaining references were not restored.")
			break
		}
		id, idErr := hex.DecodeString(ref.ID)
		hash, hashErr := hex.DecodeString(ref.SHA256)
		_, dateErr := time.Parse(time.RFC3339, ref.RetrievedAt)
		if idErr != nil || len(id) != 8 || hashErr != nil || len(hash) != sha256.Size || dateErr != nil || ref.Tool == "" {
			problem("invalid metadata or content hash")
			continue
		}
		if !validEvidenceReferencePath(ref.Path, ref.ID) {
			problem("missing or out-of-bounds session-relative path")
			continue
		}
		if ref.Bytes <= 0 || ref.Bytes > remaining {
			problem("missing or excessive recorded file size")
			continue
		}
		// os.Root resolves symlinks inside this session only, including concurrent
		// changes; no lexical normalization may turn traversal into another file.
		f, err := root.Open(ref.Path)
		if err != nil {
			problem("file missing or inaccessible within this session")
			continue
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != ref.Bytes {
			f.Close()
			problem("file type or size changed")
			continue
		}
		remaining -= ref.Bytes
		raw, err := io.ReadAll(io.LimitReader(f, ref.Bytes+1))
		f.Close()
		if err != nil || int64(len(raw)) != ref.Bytes {
			problem("file could not be read completely")
			continue
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != ref.SHA256 {
			problem("file content changed since capture")
			continue
		}
		header := fmt.Sprintf("Source: %s\nTool: %s\nRetrieved: %s\n\n", ref.URL, ref.Tool, ref.RetrievedAt)
		if !strings.HasPrefix(string(raw), header) {
			problem("provenance header does not match checkpoint")
			continue
		}
		r := ref.evidenceRecord
		r.body = strings.TrimPrefix(string(raw), header)
		r.Excerpt = trunc(r.body, 700)
		r.persistedHash, r.persistedBytes = digest, ref.Bytes
		duplicate := false
		for _, existing := range s.records {
			if existing.Path == r.Path && existing.persistedHash == r.persistedHash {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.records = append(s.records, r)
		}
	}
	if len(s.records) > 0 {
		s.writeCatalogLocked(ctx)
	}
}
func validEvidenceReferencePath(path, id string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.ContainsAny(path, "\\\x00") && filepath.Clean(path) == path && strings.HasPrefix(path, offloadDir+"/") && filepath.Base(path) == id+".txt"
}

// Workspace catalogs are presentation snapshots only. Rebuild from verified
// records after recovery instead of trusting an edited catalog on disk.
func (s *evidenceStore) writeCatalogLocked(ctx context.Context) {
	if s.backend == nil || s.dir == "" {
		return
	}
	catalog := append([]evidenceRecord(nil), s.records...)
	for i := range catalog {
		catalog[i].Excerpt = ""
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, fmt.Sprintf("catalog-%03d.json", len(s.records)))
	if err = s.backend.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: string(raw)}); err == nil {
		s.indexPath = path
	}
}
