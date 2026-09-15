package modelsettings

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/orka-oss/orka_control_layer/llm"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeOnlyActualResponsesVerifyAndPersist(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("bad provider request")
		}
		var req struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) > 0 {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"ignored tools"},"finish_reason":"stop"}]}`)
			return
		}
		var prompt string
		if json.Unmarshal(req.Messages[0].Content, &prompt) != nil {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"I cannot see images"},"finish_reason":"stop"}]}`)
			return
		}
		challenge := strings.TrimPrefix(prompt, "Reply exactly with: ")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": challenge}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}})
		w.Write(b)
	}))
	defer upstream.Close()
	s := New(filepath.Join(t.TempDir(), "storage"))
	c := Config{ID: "a", Name: "Account A", BaseURL: upstream.URL + "/v1", Models: []string{"candidate"}, Enabled: true}
	if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{c}, ActiveProfileID: "a"}, map[string]string{"a": "test-secret"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.Probe(context.Background(), "alice", "a", "candidate", []string{"text", "vision", "tools"})
	if err != nil || !v.Text.Verified || v.Vision.Verified || v.Tools.Verified || calls != 3 {
		t.Fatal("probe accepted ignored capability", v, err, calls)
	}
	got, _, err := s.Get("alice")
	if err != nil || !got.Verified["candidate"].Text.Verified {
		t.Fatal("evidence not recorded", err)
	}
	if _, err = s.Probe(context.Background(), "bob", "a", "candidate", []string{"text"}); err == nil || calls != 3 {
		t.Fatal("cross-owner probe")
	}
	if _, err = s.Probe(context.Background(), "alice", "a", "missing", []string{"text"}); err == nil || calls != 3 {
		t.Fatal("unconfigured probe")
	}
	if _, err = s.Probe(context.Background(), "alice", "a", "candidate", []string{"invented"}); err == nil || calls != 3 {
		t.Fatal("invalid capability reached provider")
	}
	p, _ := s.GetProfiles("alice")
	p.Profiles[0].Verified = map[string]Verification{"candidate": {Vision: Check{Verified: true}}}
	changed, err := s.SaveProfiles("alice", p, map[string]string{"a": "rotated"})
	if err != nil || len(changed.Profiles[0].Verified) != 0 {
		t.Fatal("new key retained old/forged evidence", err)
	}
}

func TestProbeErrorRedactionAndUnsupportedProtocol(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
		fmt.Fprint(w, "Authorization Bearer secret-provider-body")
	}))
	defer upstream.Close()
	s := New(filepath.Join(t.TempDir(), "storage"))
	p := Profiles{Profiles: []Config{{ID: "a", Name: "A", BaseURL: upstream.URL, Models: []string{"m"}, Enabled: true}}, ActiveProfileID: "a"}
	s.SaveProfiles("alice", p, map[string]string{"a": "secret-provider-body"})
	v, err := s.Probe(context.Background(), "alice", "a", "m", []string{"text"})
	if err != nil || v.Text.Verified || !strings.Contains(v.Text.Error, "401") || strings.Contains(v.Text.Error, "secret") {
		t.Fatal(v, err)
	}
	p.Profiles[0].Protocol = "anthropic"
	if _, err = s.SaveProfiles("alice", p, nil); err == nil || calls != 1 {
		t.Fatal("native protocol accepted before adapter exists")
	}
}

func answerProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []any `json:"tools"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(400)
		return
	}
	var prompt string
	msg := map[string]any{}
	finish := "stop"
	if json.Unmarshal(req.Messages[0].Content, &prompt) == nil {
		if len(req.Tools) > 0 {
			token := strings.TrimSuffix(strings.TrimPrefix(prompt, "Call report_probe with token set to "), ". Do not answer with text.")
			args, _ := json.Marshal(map[string]string{"token": token})
			msg["tool_calls"] = []any{map[string]any{"id": "test-call", "type": "function", "function": map[string]string{"name": "report_probe", "arguments": string(args)}}}
			finish = "tool_calls"
		} else {
			msg["content"] = strings.TrimPrefix(prompt, "Reply exactly with: ")
		}
	} else {
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		json.Unmarshal(req.Messages[0].Content, &parts)
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(parts[1].ImageURL.URL, "data:image/png;base64,"))
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		colors := make([]string, img.Bounds().Dx()/32)
		for i := range colors {
			red, green, blue, _ := img.At(i*32+16, 16).RGBA()
			name := "blue"
			if red > 0 && green > 0 {
				name = "yellow"
			} else if red > 0 {
				name = "red"
			} else if green > 0 {
				name = "green"
			} else if blue == 0 {
				name = "unknown"
			}
			colors[i] = name
		}
		msg["content"] = strings.Join(colors, ",")
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}})
}

type probeUsage struct{ total int }

func (p *probeUsage) AddUsage(in, out int) { p.total += in + out }
func TestProbeVerifiedCapabilitiesUseSameUsageSink(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(answerProbe))
	defer upstream.Close()
	s := New(filepath.Join(t.TempDir(), "storage"))
	s.Save("alice", Config{BaseURL: upstream.URL, Models: []string{"m"}, Enabled: true}, "")
	usage := &probeUsage{}
	v, err := s.Probe(llm.WithUsageSink(context.Background(), usage), "alice", "legacy", "m", []string{"text", "vision", "tools"})
	if err != nil || !v.Text.Verified || !v.Vision.Verified || !v.Tools.Verified || usage.total != 36 {
		t.Fatal("capabilities/usage not verified", v, err, usage.total)
	}
	p, _ := s.GetProfiles("alice")
	p.Profiles[0].Models = append(p.Profiles[0].Models, "unverified")
	p.Profiles[0].Name = "Renamed"
	saved, err := s.SaveProfiles("alice", p, nil)
	if err != nil || !saved.Profiles[0].Verified["m"].Vision.Verified || saved.Profiles[0].Verified["unverified"].Vision.Verified {
		t.Fatal("list edit invented/lost evidence", err)
	}
}
func TestProbeCannotVerifyChangedConnection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; answerProbe(w, r) }))
	defer upstream.Close()
	s := New(filepath.Join(t.TempDir(), "storage"))
	s.Save("alice", Config{BaseURL: upstream.URL, Models: []string{"m"}, Enabled: true}, "before")
	done := make(chan error, 1)
	go func() { _, err := s.Probe(context.Background(), "alice", "legacy", "m", []string{"text"}); done <- err }()
	<-started
	s.Save("alice", Config{BaseURL: upstream.URL, Models: []string{"m"}, Enabled: true}, "after")
	close(release)
	if err := <-done; err != ErrProfileChanged {
		t.Fatal("stale probe committed", err)
	}
	c, _, _ := s.Get("alice")
	if c.Verified["m"].Text.Verified {
		t.Fatal("old key verified new connection")
	}
}

type failedProbeAccountant struct{}

func (failedProbeAccountant) Begin(context.Context, llm.Request) (func(context.Context, llm.Response, error) error, error) {
	return func(context.Context, llm.Response, error) error { return fmt.Errorf("ledger unavailable") }, nil
}
func TestProbeSettlementFailureCannotExposeProviderBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "secret-provider-body")
	}))
	defer ts.Close()
	s := New(filepath.Join(t.TempDir(), "storage"))
	s.Save("alice", Config{BaseURL: ts.URL, Models: []string{"m"}, Enabled: true}, "secret-provider-body")
	_, err := s.Probe(llm.WithCallAccountant(context.Background(), failedProbeAccountant{}), "alice", "legacy", "m", []string{"text"})
	var providerError *llm.APIError
	if errors.As(err, &providerError) && strings.Contains(providerError.Body, "secret-provider-body") {
		t.Fatal("error cause leaked provider body")
	}
	if err == nil || strings.Contains(err.Error(), "secret-provider-body") {
		t.Fatal("accounting error leaked provider body", err)
	}
}
