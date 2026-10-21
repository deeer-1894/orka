package db

import (
	"context"
	"errors"
	"testing"
)

func TestMarkOnboardingRecordsKnownStepsOnce(t *testing.T) {
	base := isolatedWorkflowDB(t)
	store := &Storage{Users: base.Tasks.Database().Collection("users")}
	ctx := context.Background()
	if err := store.CreateUser(ctx, &User{Email: "new@example.com", Name: "New"}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{OnboardingTour, OnboardingTour, OnboardingDismissed} {
		if err := store.MarkOnboarding(ctx, "new@example.com", step); err != nil {
			t.Fatal(err)
		}
	}
	u, err := store.GetUser(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Onboarding) != 2 || u.Onboarding[0] != OnboardingTour || u.Onboarding[1] != OnboardingDismissed {
		t.Fatalf("onboarding = %v, want each step once", u.Onboarding)
	}
	if err := store.MarkOnboarding(ctx, "new@example.com", "anything"); err == nil {
		t.Fatal("an unknown step must be rejected")
	}
	if err := store.MarkOnboarding(ctx, "missing@example.com", OnboardingTour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: err = %v, want ErrNotFound", err)
	}
}
