// Command eval runs Orka's task suite against a live control plane and prints a
// scorecard, so a prompt change, a tool change or a model swap can be judged by
// something other than impression.
//
// It exists because nothing in the repo could answer "did that make it better?".
// The main model had just been switched to GLM with no way to tell whether
// quality moved. The suite is deliberately mechanical — files that must exist,
// strings that must appear, tools that must or must not be called — so the
// harness itself cannot drift the way an LLM judge would.
//
// The output is a JSON scorecard. Point --baseline at a previous one and it
// prints a diff, which is the actually useful mode: absolute pass rates on a
// small suite say little, but "web_search_basic regressed and tool calls
// doubled" is a decision.
//
// Usage:
//
//	eval --url http://localhost:8088 --email you@example.com --password ... \
//	     --tasks evals/tasks.yaml --out evals/results/$(date +%F).json
//	eval ... --baseline evals/results/2026-08-30.json
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type expectation struct {
	Status      string   `yaml:"status"`
	Files       []string `yaml:"files"`
	Contains    []string `yaml:"contains"`
	NotContains []string `yaml:"not_contains"`
	ToolsUsed   []string `yaml:"tools_used"`
	ToolsBanned []string `yaml:"tools_banned"`
	MaxTools    *int     `yaml:"max_tools"`
	// MaxSeconds bounds wall time. It is the only assertion that can see
	// parallelism: three independent calls made serially and made together
	// produce an IDENTICAL tool count, and differ only in elapsed time.
	MaxSeconds *float64 `yaml:"max_seconds"`
}

type followup struct {
	Prompt string      `yaml:"prompt"`
	Expect expectation `yaml:"expect"`
}

type task struct {
	ID       string      `yaml:"id"`
	Prompt   string      `yaml:"prompt"`
	Timeout  int         `yaml:"timeout_s"`
	Expect   expectation `yaml:"expect"`
	Followup *followup   `yaml:"followup"`
}

type suite struct {
	Tasks []task `yaml:"tasks"`
}

// result is one task's outcome. Metrics travel alongside pass/fail because a
// task that still passes while taking twice the tool calls is a regression the
// boolean would hide.
type result struct {
	ID      string   `json:"id"`
	Pass    bool     `json:"pass"`
	Reasons []string `json:"reasons,omitempty"`
	Status  string   `json:"status"`
	Tools   int      `json:"tools"`
	Tokens  int      `json:"tokens"`
	Seconds float64  `json:"seconds"`
}

type scorecard struct {
	At       string   `json:"at"`
	Model    string   `json:"model"`
	Passed   int      `json:"passed"`
	Total    int      `json:"total"`
	Results  []result `json:"results"`
	TotTools int      `json:"total_tools"`
	TotToks  int      `json:"total_tokens"`
}

func main() {
	var (
		url      = flag.String("url", "http://localhost:8088", "control plane base URL")
		email    = flag.String("email", os.Getenv("ORKA_EVAL_EMAIL"), "account email")
		password = flag.String("password", os.Getenv("ORKA_EVAL_PASSWORD"), "account password")
		token    = flag.String("token", os.Getenv("ORKA_EVAL_TOKEN"), "bearer token (skips login)")
		tasksF   = flag.String("tasks", "evals/tasks.yaml", "task suite")
		out      = flag.String("out", "", "write the scorecard here")
		baseline = flag.String("baseline", "", "compare against a previous scorecard")
		only     = flag.String("only", "", "run just this task id")
		model    = flag.String("model", "", "selected_version to run on: \"\" main, mini, auto, or a model name; also recorded in the scorecard")
	)
	flag.Parse()

	// Check the flags that only matter at the END before spending ten minutes of
	// model calls to reach them. A baseline that cannot be read, or a run whose
	// scorecard is never written, wastes the whole suite.
	var prev scorecard
	if *baseline != "" {
		if err := readJSON(*baseline, &prev); err != nil {
			fmt.Fprintf(os.Stderr, "eval: cannot read baseline %s: %v\n", *baseline, err)
			fmt.Fprintf(os.Stderr, "      create one first with:  --out %s\n", *baseline)
			os.Exit(2)
		}
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "eval: note — no --out given, so this scorecard will not be saved.")
	}

	sc, err := run(*url, *email, *password, *token, *tasksF, *only, *model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(2)
	}
	report(sc)
	if *out != "" {
		if err := writeJSON(*out, sc); err != nil {
			fmt.Fprintln(os.Stderr, "eval: write:", err)
		} else {
			fmt.Printf("\nscorecard → %s\n", *out)
		}
	}
	if *baseline != "" {
		compare(prev, sc) // already loaded and validated before the suite ran
	}
	// A failing suite exits non-zero so CI can gate on it.
	if sc.Passed < sc.Total {
		os.Exit(1)
	}
}

func run(base, email, password, token, tasksFile, only, model string) (scorecard, error) { //nolint:revive // argument-limit
	var s suite
	raw, err := os.ReadFile(tasksFile)
	if err != nil {
		return scorecard{}, err
	}
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return scorecard{}, fmt.Errorf("parse %s: %w", tasksFile, err)
	}
	c := &client{base: base, token: token, model: model}
	if c.token == "" {
		if email == "" || password == "" {
			return scorecard{}, fmt.Errorf("need --token, or --email and --password")
		}
		if err := c.login(email, password); err != nil {
			return scorecard{}, err
		}
	}

	sc := scorecard{At: time.Now().Format(time.RFC3339), Model: model}
	for _, t := range s.Tasks {
		if only != "" && t.ID != only {
			continue
		}
		fmt.Printf("▸ %-22s ", t.ID)
		r := c.runTask(t)
		sc.Results = append(sc.Results, r)
		sc.Total++
		sc.TotTools += r.Tools
		sc.TotToks += r.Tokens
		if r.Pass {
			sc.Passed++
			fmt.Printf("PASS  %.0fs  %d tools  %d tok\n", r.Seconds, r.Tools, r.Tokens)
		} else {
			fmt.Printf("FAIL  %.0fs  %s\n", r.Seconds, strings.Join(r.Reasons, "; "))
		}
	}
	return sc, nil
}

// runTask executes one task (plus its follow-up turn, when it has one) in a
// fresh conversation, so tasks cannot contaminate each other's memory.
func (c *client) runTask(t task) result {
	timeout := time.Duration(t.Timeout) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	conv := fmt.Sprintf("eval_%s_%d", t.ID, time.Now().UnixNano())
	// Delete the files this task asserts BEFORE running it. Without this the
	// suite grades itself on the previous run's leftovers: a task that no longer
	// writes anything still "passes" because the file is already there, which is
	// precisely the regression the suite exists to catch.
	c.clearExpected(t)
	started := time.Now()

	r := result{ID: t.ID, Pass: true}
	turn, err := c.turn(conv, t.Prompt, timeout)
	r.Seconds = time.Since(started).Seconds()
	if err != nil {
		return result{ID: t.ID, Seconds: r.Seconds, Reasons: []string{err.Error()}}
	}
	r.Status, r.Tools, r.Tokens = turn.status, turn.tools, turn.tokens
	r.Reasons = check(c, t.Expect, turn)

	if t.Followup != nil && len(r.Reasons) == 0 {
		f, ferr := c.turn(conv, t.Followup.Prompt, timeout)
		r.Seconds = time.Since(started).Seconds()
		if ferr != nil {
			r.Reasons = append(r.Reasons, "followup: "+ferr.Error())
		} else {
			r.Tools += f.tools
			r.Tokens += f.tokens
			for _, why := range check(c, t.Followup.Expect, f) {
				r.Reasons = append(r.Reasons, "followup: "+why)
			}
		}
	}
	r.Pass = len(r.Reasons) == 0
	return r
}

// check evaluates one expectation, returning every reason it failed rather than
// the first — one run is expensive, so it should report everything it can.
func check(c *client, e expectation, t *turnResult) []string {
	var why []string
	want := e.Status
	if want == "" {
		want = "done"
	}
	if t.status != want {
		why = append(why, fmt.Sprintf("status=%s want %s", t.status, want))
	}
	low := strings.ToLower(t.answer)
	for _, sub := range e.Contains {
		if !strings.Contains(low, strings.ToLower(sub)) {
			why = append(why, "answer missing "+quote(sub))
		}
	}
	for _, sub := range e.NotContains {
		if strings.Contains(low, strings.ToLower(sub)) {
			why = append(why, "answer contains banned "+quote(sub))
		}
	}
	for _, f := range e.Files {
		if !c.fileExists(f) {
			why = append(why, "missing file "+quote(f))
		}
	}
	for _, name := range e.ToolsUsed {
		if !t.used[name] {
			why = append(why, "never called "+name)
		}
	}
	for _, name := range e.ToolsBanned {
		if t.used[name] {
			why = append(why, "called banned tool "+name)
		}
	}
	if e.MaxTools != nil && t.tools > *e.MaxTools {
		why = append(why, fmt.Sprintf("%d tool calls > max %d", t.tools, *e.MaxTools))
	}
	if e.MaxSeconds != nil && t.seconds > *e.MaxSeconds {
		why = append(why, fmt.Sprintf("took %.0fs > max %.0fs (work that should run in parallel is being serialised)",
			t.seconds, *e.MaxSeconds))
	}
	return why
}

func report(sc scorecard) {
	fmt.Printf("\n%d/%d passed · %d tool calls · %d tokens\n",
		sc.Passed, sc.Total, sc.TotTools, sc.TotToks)
}

// compare is the mode that earns the suite its keep. An absolute pass rate on
// nine tasks says little; a task that flipped, or a cost that moved, is a
// decision about the change that caused it.
func compare(prev, cur scorecard) {
	fmt.Printf("\n── vs baseline (%s) ──\n", prev.At)
	was := map[string]result{}
	for _, r := range prev.Results {
		was[r.ID] = r
	}
	var flips, drift []string
	for _, r := range cur.Results {
		p, ok := was[r.ID]
		if !ok {
			flips = append(flips, "+ "+r.ID+" (new)")
			continue
		}
		switch {
		case p.Pass && !r.Pass:
			flips = append(flips, "✗ "+r.ID+" REGRESSED: "+strings.Join(r.Reasons, "; "))
		case !p.Pass && r.Pass:
			flips = append(flips, "✓ "+r.ID+" fixed")
		}
		// Flag cost changes on tasks that still pass — the quiet kind of
		// regression, where the answer is right and getting it got expensive.
		if p.Pass && r.Pass && p.Tools > 0 && r.Tools >= p.Tools*2 {
			drift = append(drift, fmt.Sprintf("  %s: %d → %d tool calls", r.ID, p.Tools, r.Tools))
		}
	}
	sort.Strings(flips)
	if len(flips) == 0 {
		fmt.Println("no pass/fail changes")
	}
	for _, l := range flips {
		fmt.Println(l)
	}
	if len(drift) > 0 {
		fmt.Println("cost drift (still passing):")
		for _, l := range drift {
			fmt.Println(l)
		}
	}
	fmt.Printf("pass %d/%d → %d/%d · tokens %d → %d\n",
		prev.Passed, prev.Total, cur.Passed, cur.Total, prev.TotToks, cur.TotToks)
}

// ---- control-plane client ----

type client struct {
	base  string
	token string
	// model is the selected_version every task runs on, so a suite can be scored
	// per tier. The per-round-trip floor is ~15s on the strong tier here, which
	// makes "which tier" the dominant cost of a multi-step task.
	model string
}

type turnResult struct {
	answer  string
	status  string
	tools   int
	tokens  int
	seconds float64
	used    map[string]bool
}

func (c *client) login(email, password string) error {
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := c.postJSON("/api/v1/controller/auth/login",
		map[string]string{"email": email, "password": password}, &out); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if out.Data.Token == "" {
		return fmt.Errorf("login: no token returned")
	}
	c.token = out.Data.Token
	return nil
}

// turn sends one message and consumes the SSE stream to the end, tallying what
// the agent did. Reading the stream (rather than polling the run record) is
// what makes tool usage observable per turn.
func (c *client) turn(conv, prompt string, timeout time.Duration) (*turnResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	body, _ := json.Marshal(map[string]any{
		"message": prompt, "conversation_id": conv, "confirm_risky": false,
		"selected_version": c.model,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		c.base+"/api/v1/controller/chat/run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("run: HTTP %d", resp.StatusCode)
	}

	begun := time.Now()
	t := &turnResult{used: map[string]bool{}, status: "incomplete"}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20) // tool results can be large
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var m struct {
			Type    string         `json:"type"`
			Role    string         `json:"role"`
			Action  string         `json:"action"`
			Content string         `json:"content"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &m) != nil {
			continue
		}
		switch m.Type {
		case "chat":
			if m.Role == "assistant" {
				t.answer = m.Content // last assistant turn wins
			}
		case "tool":
			t.tools++
			if n, ok := m.Payload["tool"].(string); ok {
				t.used[n] = true
			}
		case "task":
			if m.Action == "done" || m.Action == "failed" {
				t.status = m.Action
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}
	t.seconds = time.Since(begun).Seconds()
	// The stream reports done/failed; the run record holds the finer verdict
	// (partial), which is precisely what several of these tasks assert on.
	if st, tok := c.runVerdict(conv); st != "" {
		t.status = st
		if tok > 0 {
			t.tokens = tok
		}
	}
	return t, nil
}

// runVerdict reads back the recorded status for a conversation's newest run.
func (c *client) runVerdict(conv string) (string, int) {
	var out struct {
		Data struct {
			Runs []struct {
				Status string `json:"status"`
				Tokens int    `json:"tokens"`
			} `json:"runs"`
		} `json:"data"`
	}
	if c.postJSON("/api/v1/controller/run/list", map[string]any{"conversation_id": conv}, &out) != nil {
		return "", 0
	}
	if len(out.Data.Runs) == 0 {
		return "", 0
	}
	return out.Data.Runs[0].Status, out.Data.Runs[0].Tokens
}

// clearExpected removes every file the task (and its follow-up) asserts, so
// each run starts from a known-empty state.
func (c *client) clearExpected(t task) {
	paths := append([]string(nil), t.Expect.Files...)
	if t.Followup != nil {
		paths = append(paths, t.Followup.Expect.Files...)
	}
	for _, p := range paths {
		var out any
		_ = c.postJSON("/api/v1/controller/file/delete", map[string]string{"path": p}, &out)
	}
}

func (c *client) fileExists(path string) bool {
	var out struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	dir, name := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	if c.postJSON("/api/v1/controller/file/list", map[string]string{"path": strings.TrimSuffix(dir, "/")}, &out) != nil {
		return false
	}
	for _, e := range out.Data {
		if e.Name == name {
			return true
		}
	}
	return false
}

func (c *client) postJSON(path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d on %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---- small helpers ----

func quote(s string) string { return "\"" + s + "\"" }

func writeJSON(path string, v any) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
