package service

import "testing"

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
	for _, want := range []string{"完全相同", "已经成功", "换一种方式"} {
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
