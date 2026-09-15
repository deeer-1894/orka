package db

import (
	"context"
	"errors"
	"testing"
)

func TestWorkflowMongoSnapshotAndOwnership(t *testing.T) {
	store := isolatedWorkflowDB(t)
	ctx := context.Background()
	r := WorkflowRun{RunID: "parent", OwnerEmail: "alice", WorkflowID: "wf", Status: RunRunning, Steps: []WorkflowStepRun{{Name: "a.b", Status: RunRunning}}}
	if err := store.SaveWorkflowRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Status = RunPartial
	r.Steps[0].Status = RunPartial
	if err := store.SaveWorkflowRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetWorkflowRun(ctx, "parent", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RunPartial || got.Steps[0].Status != RunPartial {
		t.Fatalf("snapshot=%+v", got)
	}
	if _, err := store.GetWorkflowRun(ctx, "parent", "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
}
