package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionRemovesOnlyOldJournals(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, ".orka_journals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old, fresh := filepath.Join(dir, "old.json"), filepath.Join(dir, "fresh.json")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-(journalDays + 1) * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	r := &Retention{Base: base}
	if n := r.sweepJournals(time.Now().Add(-journalDays * 24 * time.Hour)); n != 1 {
		t.Fatalf("removed %d journals, want 1", n)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a recent journal (a run that may still resume) must stay")
	}
}

func TestRetentionDaysFromEnv(t *testing.T) {
	for v, want := range map[string]int{"": 0, "abc": 0, "-3": 0, "0": 0, " 365 ": 365} {
		t.Setenv("ORKA_RETENTION_DAYS", v)
		if got := RetentionDaysFromEnv(); got != want {
			t.Errorf("%q → %d, want %d", v, got, want)
		}
	}
}
