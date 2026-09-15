package modelsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/modelprofile"
)

type probePolicyAccountant struct {
	t      *testing.T
	effort string
	cap    int
	calls  int
}

func (a *probePolicyAccountant) Begin(ctx context.Context, req llm.Request) (func(context.Context, llm.Response, error) error, error) {
	a.calls++
	if req.ReasoningEffort != a.effort || req.MaxTokens != a.cap {
		a.t.Errorf("accounting saw effort=%q max_tokens=%d; want %q/%d", req.ReasoningEffort, req.MaxTokens, a.effort, a.cap)
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 45*time.Second {
		a.t.Error("probe lost its 45-second total deadline")
	}
	return func(context.Context, llm.Response, error) error { return nil }, nil
}

func TestProbeUsesExplicitReasoningPolicyBeforeAccountingAndDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort string
		cap                 int
	}{
		{"flash low", "glm-5.3-flash", "low", 2048},
		{"unknown explicit", "custom-model", "medium", 2048},
		{"no model inference", "glm-5.3-flash", "", 128},
		{"reasoning disabled", "custom-model", "none", 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var wire struct {
					ReasoningEffort string `json:"reasoning_effort"`
					MaxTokens       int    `json:"max_tokens"`
				}
				if err := json.Unmarshal(body, &wire); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if wire.ReasoningEffort != tc.effort || wire.MaxTokens != tc.cap {
					t.Errorf("provider saw effort=%q max_tokens=%d; want %q/%d", wire.ReasoningEffort, wire.MaxTokens, tc.effort, tc.cap)
					fmt.Fprint(w, `{"choices":[{"message":{"content":"","reasoning_content":"still thinking"},"finish_reason":"length"}]}`)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				answerProbe(w, r)
			}))
			defer upstream.Close()
			s := New(filepath.Join(t.TempDir(), "storage"))
			c := Config{ID: "a", Name: "A", BaseURL: upstream.URL, Models: []string{tc.model}, Enabled: true,
				Policies: map[string]modelprofile.CallPolicy{tc.model: {ReasoningEffort: tc.effort, MaxTokens: 16384, TimeoutSeconds: 300}}}
			if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{c}, ActiveProfileID: "a"}, nil); err != nil {
				t.Fatal(err)
			}
			accountant := &probePolicyAccountant{t: t, effort: tc.effort, cap: tc.cap}
			v, err := s.Probe(llm.WithCallAccountant(context.Background(), accountant), "alice", "a", tc.model, []string{"text", "vision", "tools"})
			if err != nil || !v.Text.Verified || !v.Vision.Verified || !v.Tools.Verified || calls != 3 || accountant.calls != 3 {
				t.Fatalf("explicit policy did not reach all challenges: %+v err=%v provider=%d accountant=%d", v, err, calls, accountant.calls)
			}
		})
	}
}
