package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// verifier downloads Files from the conversation workspace and runs Script
// (Python, given the download directory) over them. Scripts only parse the
// files; they never execute model-written code on the evaluating host. Each
// stdout line starting with "FAIL:" is one reason; a non-zero exit fails.
type verifier struct {
	Script string   `yaml:"script"`
	Files  []string `yaml:"files"`
}

// resolveVerifierScripts makes relative script paths relative to the suite file.
func resolveVerifierScripts(tasks []task, suiteDir string) {
	for _, t := range tasks {
		// t is a copy, but Verify is a pointer shared with the suite.
		for _, e := range append([]*expectation{&t.Expect}, laterExpects(t)...) {
			if e != nil && e.Verify != nil && !filepath.IsAbs(e.Verify.Script) {
				e.Verify.Script = filepath.Join(suiteDir, e.Verify.Script)
			}
		}
	}
}

func laterExpects(t task) []*expectation {
	// Verify is a pointer shared with the suite, so copies resolve it too.
	turns := t.laterTurns()
	out := make([]*expectation, len(turns))
	for i := range turns {
		out[i] = &turns[i].Expect
	}
	return out
}

func runVerifier(c *client, conv string, v *verifier) []string {
	dir, err := os.MkdirTemp("", "orka-eval-verify-")
	if err != nil {
		return []string{"verify: " + err.Error()}
	}
	defer os.RemoveAll(dir)
	var why []string
	for _, p := range v.Files {
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if !strings.HasPrefix(dst, dir+string(os.PathSeparator)) {
			why = append(why, "verify: unsafe path "+quote(p))
			continue
		}
		body, ferr := c.readFile(conv, p)
		if ferr != nil {
			why = append(why, "verify: cannot download "+quote(p)+": "+ferr.Error())
			continue
		}
		werr := os.MkdirAll(filepath.Dir(dst), 0o755)
		if werr == nil {
			werr = os.WriteFile(dst, []byte(body), 0o644)
		}
		if werr != nil {
			why = append(why, "verify: "+werr.Error())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", v.Script, dir).CombinedOutput()
	failed := false
	for line := range strings.SplitSeq(string(out), "\n") {
		if reason, ok := strings.CutPrefix(strings.TrimSpace(line), "FAIL:"); ok {
			why = append(why, "verify: "+strings.TrimSpace(reason))
			failed = true
		}
	}
	if err != nil && !failed {
		why = append(why, "verify: "+err.Error()+": "+trimTail(string(out), 400))
	}
	return why
}

func trimTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
