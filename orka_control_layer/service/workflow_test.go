package service

import "github.com/orka-oss/orka_control_layer/db"

import "testing"

func TestNormalizeDAG_LinearByDefault(t *testing.T) {
	steps := []db.WorkflowStep{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got := normalizeDAG(steps)
	if len(got[0].DependsOn) != 0 {
		t.Fatalf("first step should be an entry node, got %v", got[0].DependsOn)
	}
	if got[1].DependsOn[0] != "a" || got[2].DependsOn[0] != "b" {
		t.Fatalf("implicit chain wrong: %v %v", got[1].DependsOn, got[2].DependsOn)
	}
}

func TestNormalizeDAG_RespectsExplicit(t *testing.T) {
	steps := []db.WorkflowStep{{Name: "a"}, {Name: "b", DependsOn: []string{"a"}}, {Name: "c", DependsOn: []string{"a"}}}
	got := normalizeDAG(steps)
	// b and c both depend only on a (a diamond fan-out) — must be preserved, not
	// rewritten into a b→c chain.
	if len(got[2].DependsOn) != 1 || got[2].DependsOn[0] != "a" {
		t.Fatalf("explicit deps overwritten: %v", got[2].DependsOn)
	}
}

func TestEvalRunIf(t *testing.T) {
	out := map[string]string{"research": "we FOUND a match", "score": "0.9"}
	cases := []struct {
		expr string
		want bool
	}{
		{"", true},
		{"research contains FOUND", true},
		{"research contains MISSING", false},
		{"research !contains MISSING", true},
		{`score == 0.9`, true},
		{`score != 0.9`, false},
		{"bogus contains x", false}, // bogus step → empty output, no match
		{"garbage expression", true}, // unparseable → fail-open
	}
	for _, c := range cases {
		if got := evalRunIf(c.expr, out); got != c.want {
			t.Errorf("evalRunIf(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestSubstitute(t *testing.T) {
	out := map[string]string{"a": "RESULT"}
	if got := substitute("use {{a}} here", out); got != "use RESULT here" {
		t.Fatalf("substitute = %q", got)
	}
	if got := substitute("no refs", out); got != "no refs" {
		t.Fatalf("untouched = %q", got)
	}
}

func TestOnErrorPolicy(t *testing.T) {
	if !onErrorStops("") || !onErrorStops("stop") {
		t.Fatal("default/stop should stop")
	}
	if onErrorStops("continue") || onErrorStops("retry:3") {
		t.Fatal("continue/retry should not stop")
	}
	if onErrorRetries("retry:3") != 3 || onErrorRetries("stop") != 0 || onErrorRetries("retry:x") != 0 {
		t.Fatal("retry parsing wrong")
	}
}
