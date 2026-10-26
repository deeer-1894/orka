package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingOAuthIsSingleUseAndGrantSavesAreConditional(t *testing.T) {
	s := isolatedWorkflowDB(t)
	d := s.Tasks.Database()
	s.OAuthPending, s.Connectors = d.Collection("oauth_pending"), d.Collection("connectors")
	ctx := context.Background()

	if err := s.SavePendingOAuth(ctx, &OAuthPending{State: "s1", Owner: "a@x", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePendingOAuth(ctx, &OAuthPending{State: "old", Owner: "a@x", ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPendingOAuth(ctx, "a@x"); n != 1 {
		t.Fatalf("open authorizations = %d, want 1 (expired ones do not count)", n)
	}
	if p, err := s.TakePendingOAuth(ctx, "s1"); err != nil || p.Owner != "a@x" {
		t.Fatalf("take: %+v %v", p, err)
	}
	if _, err := s.TakePendingOAuth(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second take = %v, want not found", err)
	}
	if _, err := s.TakePendingOAuth(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired state = %v, want not found", err)
	}

	if err := s.CreateConnector(ctx, &Connector{ConnectorID: "c1", OwnerEmail: "a@x", OAuth: &ConnectorOAuth{RefreshToken: "r1"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveConnectorOAuthIf(ctx, "c1", "a@x", "r1", &ConnectorOAuth{RefreshToken: "r2"}); err != nil || !ok {
		t.Fatalf("first save: %v %v", ok, err)
	}
	if ok, _ := s.SaveConnectorOAuthIf(ctx, "c1", "a@x", "r1", &ConnectorOAuth{RefreshToken: "r3"}); ok {
		t.Fatal("a save based on a rotated token must lose")
	}
	if c, err := s.GetConnector(ctx, "c1", "a@x"); err != nil || c.OAuth.RefreshToken != "r2" {
		t.Fatalf("stored grant %+v %v", c, err)
	}
	if _, err := s.GetConnector(ctx, "c1", "b@x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("another user's connector is not found")
	}
}
