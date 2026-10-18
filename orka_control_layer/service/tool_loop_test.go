package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestBrowserRepeatedFailureIgnoresElapsedTimeOnly(t *testing.T) {
	d := newLoopDetector()
	key := "browser\x00{\"action\":\"evaluate\",\"expression\":\"same invalid code\"}"
	for i := 1; i <= 3; i++ {
		result := fmt.Sprintf(`{"ok":false,"action":"evaluate","page_id":"one","page_epoch":2,"elapsed_ms":%d,"error":{"code":"script_error","message":"Browser page script failed."}}`, i*13)
		note := d.observe(key, result)
		if (note != "") != (i == 3) {
			t.Fatalf("attempt %d: %q", i, note)
		}
	}
	// A different page epoch/error is new evidence, even with identical arguments.
	if note := d.observe(key, `{"ok":false,"action":"evaluate","page_id":"one","page_epoch":3,"elapsed_ms":40,"error":{"code":"script_error","message":"Browser page script failed."}}`); note != "" {
		t.Fatal("new page state treated as unchanged failure")
	}
}

func TestBrowserLoopNormalizationPreservesOtherEvidence(t *testing.T) {
	for _, key := range []string{"browser\x00{}", "other\x00{}"} {
		d := newLoopDetector()
		for i := 1; i <= 4; i++ {
			result := fmt.Sprintf(`{"ok":true,"elapsed_ms":%d,"value":%d}`, i, i)
			if note := d.observe(key, result); note != "" {
				t.Fatal("changing successful result was normalized")
			}
		}
	}
	original := `{"ok":false,"elapsed_ms":1,"error":{"code":"script_error"}}`
	if loopObservation("other\x00{}", original) != original {
		t.Fatal("unrelated tool was normalized")
	}
	for _, raw := range []string{"not json", `{"ok":false}`, `{"ok":false,"error":"failed"}`, original + " trailing evidence"} {
		if loopObservation("browser\x00{}", raw) != raw {
			t.Fatal("non-contract result changed")
		}
	}
}

// The run that motivated this produced all nine of its deliverables and then
// called file_list five times with identical arguments, getting identical output
// each time, until the budget ran out — filed partial with "write the report"
// still open, though the report was on disk.
func TestLoopDetectorNudgesOnIdenticalRepeats(t *testing.T) {
	d := newLoopDetector()
	key, out := "file_list\x00{}", "a\tb\tc"

	// Two identical calls are ordinary: re-reading a file you just wrote is good
	// practice, and a nudge there would be noise.
	for i := 1; i < repeatBeforeNudge; i++ {
		if note := d.observe(key, out); note != "" {
			t.Fatalf("nudged on call %d: %s", i, note)
		}
	}
	note := d.observe(key, out)
	if note == "" {
		t.Fatal("no nudge after the loop became obvious")
	}
	for _, want := range []string{"完全相同", "不代表操作成功", "换一种方式"} {
		if !contains(note, want) {
			t.Errorf("nudge is missing %q: %s", want, note)
		}
	}
}

// Polling something that is genuinely changing is work, not a loop. A changed
// result must reset the count, or a legitimate watch would be told to stop.
func TestLoopDetectorResetsWhenResultChanges(t *testing.T) {
	d := newLoopDetector()
	key := "file_list\x00{}"
	for i := 0; i < repeatBeforeNudge+2; i++ {
		if note := d.observe(key, "result "+itoa(i)); note != "" {
			t.Fatalf("nudged a call whose result changed every time: %s", note)
		}
	}
}

// Different arguments are different questions.
func TestLoopDetectorIsPerArguments(t *testing.T) {
	d := newLoopDetector()
	for i := 0; i < repeatBeforeNudge+2; i++ {
		if note := d.observe("file_read\x00{\"path\":\"f"+itoa(i)+"\"}", "same"); note != "" {
			t.Fatalf("nudged distinct calls: %s", note)
		}
	}
}

// Once looping, every further identical call keeps saying so — the model may
// need more than one turn to change course.
func TestLoopDetectorKeepsNudging(t *testing.T) {
	d := newLoopDetector()
	key := "x\x00{}"
	for i := 0; i < repeatBeforeNudge; i++ {
		d.observe(key, "same")
	}
	if d.observe(key, "same") == "" {
		t.Fatal("stopped nudging while the loop continued")
	}
}

func TestLoopDetectorNilIsInert(t *testing.T) {
	var d *loopDetector
	if note := d.observe("k", "v"); note != "" {
		t.Fatalf("nil detector returned %q", note)
	}
}

func TestRepeatedFailureNeverClaimsSuccess(t *testing.T) {
	d := newLoopDetector()
	var note string
	for i := 0; i < repeatBeforeNudge; i++ {
		note = d.observe("file_read", `{"error":"not found"}`)
	}
	if strings.Contains(note, "已经成功") {
		t.Fatal("identical failures were presented as successful work")
	}
}
