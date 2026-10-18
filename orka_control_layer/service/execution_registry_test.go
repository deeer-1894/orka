package service

import (
	"context"
	"errors"
	"testing"
)

func TestExecutionAdmissionExcludesDuplicateAndReleasesExactRun(t *testing.T) {
	s := &ChatService{}
	ctx, release, err := s.AdmitExecution(context.Background(), "owner", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitExecution(context.Background(), "owner", "conv", ""); !errors.Is(err, ErrExecutionActive) {
		t.Fatalf("duplicate: %v", err)
	}
	id := ExecutionID(ctx)
	if id == "" {
		t.Fatal("no execution identity")
	}
	release()
	next, releaseNext, err := s.AdmitExecution(context.Background(), "owner", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseNext()
	release()
	if next.Err() != nil || !s.Kill("conv") {
		t.Fatal("stale release affected successor")
	}
}
func TestExecutionChildrenHaveIndependentCancellation(t *testing.T) {
	s := &ChatService{}
	parent, release, err := s.AdmitExecution(context.Background(), "owner", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	child1, r1, err := s.AdmitExecution(WithExecutionIdentity(parent, "child1", ExecutionID(parent)), "owner", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	child2, r2, err := s.AdmitExecution(WithExecutionIdentity(parent, "child2", ExecutionID(parent)), "owner", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	defer r2()
	if !s.Kill("child1") || child1.Err() == nil || child2.Err() != nil {
		t.Fatal("child cancellation crossed execution")
	}
	r1()
	if !s.Kill("conv") || parent.Err() == nil || child2.Err() == nil {
		t.Fatal("parent cancellation did not reach remaining child")
	}
}
func TestExecutionAdmissionCannotReuseOtherOwnerLease(t *testing.T) {
	s := &ChatService{}
	ctx, release, err := s.AdmitExecution(context.Background(), "a", "conv", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err = s.AdmitExecution(ctx, "b", "conv", ""); err == nil {
		t.Fatal("cross-owner lease reuse")
	}
}
