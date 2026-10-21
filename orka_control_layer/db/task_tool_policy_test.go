package db

import (
	"slices"
	"testing"
)

func TestTaskToolPolicyNormalize(t *testing.T) {
	got, err := TaskToolPolicy{Allow: []string{CapCode, CapWorkspaceWrite, CapCode}}.Normalize()
	if err != nil || !slices.Equal(got.Allow, []string{CapCode, CapWorkspaceWrite}) {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := (TaskToolPolicy{}).Normalize(); err != nil || got.Allow == nil || len(got.Allow) != 0 {
		t.Fatalf("read-only must stay an explicit empty list: %#v %v", got, err)
	}
	if _, err := (TaskToolPolicy{Allow: []string{"http_request"}}).Normalize(); err == nil {
		t.Fatal("unknown capability accepted")
	}
	if d := DefaultTaskToolPolicy(); !slices.Equal(d.Allow, []string{CapWorkspaceWrite}) {
		t.Fatalf("default changed: %v", d)
	}
}
