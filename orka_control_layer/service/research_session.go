package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/orka-oss/orka_core/toolargs"
)

const defaultResearchMaxCalls = 40

// researchSession owns one execution's retrieval allowance and in-flight calls.
// It never caches mutations, generic HTTP requests, or another run's results.
// Evidence remains readable after the remote allowance is exhausted.
type researchSession struct {
	mu              sync.Mutex
	pending         map[string]*researchCall
	cache           map[string]string
	calls, maxCalls int
	budget          *runBudget
	evidence        *evidenceStore
}

type researchCall struct {
	done chan struct{}
	out  string
	err  error
}

func newResearchSession(backend filesystem.Backend, dir string, budget *runBudget, maxCalls int) *researchSession {
	if maxCalls <= 0 {
		maxCalls = defaultResearchMaxCalls
	}
	return &researchSession{pending: make(map[string]*researchCall), cache: make(map[string]string), maxCalls: maxCalls, budget: budget, evidence: newEvidenceStore(backend, dir)}
}

func isResearchTool(name string) bool {
	switch name {
	case "web_search", "fetch_url", "discover_docs", "read_section":
		return true
	}
	return false
}

// researchKey canonicalizes transport-irrelevant differences only. Query values
// and read_section fragments remain intact because they may select content.
func researchKey(name string, args map[string]any) string {
	canonical := make(map[string]any, len(args))
	for k, v := range args {
		canonical[k] = v
	}
	if raw, ok := canonical["url"].(string); ok {
		raw = strings.TrimSpace(raw)
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		if u, err := url.Parse(raw); err == nil {
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			if name == "fetch_url" {
				u.Fragment = ""
			}
			canonical["url"] = u.String()
		}
	}
	b, _ := json.Marshal(canonical)
	return name + "\x00" + string(b)
}

func (s *researchSession) invoke(ctx context.Context, name string, args map[string]any, call func() (string, error)) (string, error) {
	if s == nil {
		return call()
	}
	if !isResearchTool(name) {
		out, err := call()
		if name == "file_read" && err == nil && usefulResearchResult(out) {
			out = s.evidence.reduceRepeatedRead(ctx, toolargs.Path(args), out)
		}
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key := researchKey(name, args)
	s.mu.Lock()
	if out, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return "[cached evidence; no new network request]\n" + out, nil
	}
	if p := s.pending[key]; p != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-p.done:
			return p.out, p.err
		}
	}
	if s.atLimitLocked() {
		s.mu.Unlock()
		return researchLimitNotice, nil
	}
	p := &researchCall{done: make(chan struct{})}
	s.pending[key] = p
	s.calls++ // reserve before releasing the lock, including parallel batches
	s.mu.Unlock()

	out, err := call()
	success := err == nil && usefulResearchResult(out)
	if success {
		out = s.evidence.capture(ctx, key, name, args, out)
	}
	s.mu.Lock()
	p.out, p.err = out, err
	if success {
		s.cache[key] = out
	}
	delete(s.pending, key)
	close(p.done)
	s.mu.Unlock()
	return out, err
}

func usefulResearchResult(out string) bool {
	out = strings.TrimSpace(out)
	return out != "" && !strings.HasPrefix(out, "tool error") && !strings.HasPrefix(out, "tool call failed") && !strings.Contains(out, "Search is temporarily unavailable")
}

func (s *researchSession) atLimitLocked() bool {
	if s.calls >= s.maxCalls {
		return true
	}
	// The run's immutable total limit is shared with the usage meter. Reserve
	// half for synthesis/execution instead of letting retrieval consume it all.
	return s.budget != nil && s.budget.maxTokens > 0 && s.budget.spentTokens() >= s.budget.maxTokens/2
}

func (s *researchSession) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := fmt.Sprintf("External retrieval: %d/%d calls. %s", s.calls, s.maxCalls, s.evidence.summary())
	if s.atLimitLocked() {
		msg += "\n" + researchLimitNotice
	}
	return msg
}

const researchLimitNotice = "[retrieval budget reserved for delivery] Stop external searches and new page reads. Use search_evidence or file_read for collected sources; save the research with citations, mark actual plan progress, then execute and verify the remaining deliverables. Explicitly label any evidence gaps; never invent missing facts. This is not the end of the task: file tools, shell and other execution tools remain available."

type researchSessionKey struct{}

func withResearchSession(ctx context.Context, s *researchSession) context.Context {
	// Direct tool callers also get independent delivery history. Agent runners
	// replace this fallback in BeforeAgent for each parent/delegate execution.
	return withEvidenceReadTracker(context.WithValue(ctx, researchSessionKey{}, s))
}
func researchFrom(ctx context.Context) *researchSession {
	s, _ := ctx.Value(researchSessionKey{}).(*researchSession)
	return s
}
