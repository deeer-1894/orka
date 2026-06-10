package message_utils

import (
	"strings"
	"testing"
)

func TestStripHeavy_OmitsLargeScreenshot(t *testing.T) {
	big := strings.Repeat("A", 5000)
	in := map[string]any{"type": "screenshot", "data": big, "mode": "vision"}
	out, ok := stripHeavy(in).(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if d, _ := out["data"].(string); !strings.HasPrefix(d, "<omitted") {
		t.Fatalf("data not stripped: %q", d)
	}
	if out["mode"] != "vision" || out["type"] != "screenshot" {
		t.Fatalf("other fields lost: %+v", out)
	}
	// input must not be mutated
	if in["data"].(string) != big {
		t.Fatal("stripHeavy mutated the input payload")
	}
}

func TestStripHeavy_KeepsSmallPayloads(t *testing.T) {
	in := map[string]any{"tool": "file_write", "result": "ok"}
	if got := stripHeavy(in); !sameMap(got.(map[string]any), in) {
		t.Fatalf("small payload changed: %+v", got)
	}
	// short data field is kept as-is
	in2 := map[string]any{"data": "short"}
	if stripHeavy(in2).(map[string]any)["data"] != "short" {
		t.Fatal("short data should be kept")
	}
	// non-map payloads pass through
	if stripHeavy("plain") != "plain" {
		t.Fatal("non-map payload changed")
	}
}

func sameMap(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
