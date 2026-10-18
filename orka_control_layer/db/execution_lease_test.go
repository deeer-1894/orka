package db

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/messages"
	"go.mongodb.org/mongo-driver/bson"
)

func TestExecutionLeaseMongoContentionAndOwnerIsolation(t *testing.T) {
	store := isolatedWorkflowDB(t)
	store.Conversations = store.Tasks.Database().Collection("conversations")
	ctx := context.Background()
	if _, err := store.Conversations.InsertOne(ctx, bson.M{"owner_email": "alice", "conversation_id": "conv"}); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.ClaimConversationExecution(ctx, "alice", "conv", messages.NewID(), time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("conversation lease winners=%d", wins.Load())
	}
	if ok, err := store.ClaimConversationExecution(ctx, "bob", "conv", "foreign", time.Minute); ok || err != nil {
		t.Fatalf("foreign owner claimed conversation: %v %v", ok, err)
	}
}
func TestExecutionLeaseMongoExpiryAndStaleOwnerCannotReleaseSuccessor(t *testing.T) {
	store := isolatedWorkflowDB(t)
	store.Conversations = store.Tasks.Database().Collection("conversations")
	ctx := context.Background()
	if _, err := store.Conversations.InsertOne(ctx, bson.M{"owner_email": "alice", "conversation_id": "conv"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimConversationExecution(ctx, "alice", "conv", "old", time.Minute); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// Expire only this fixture's lease; no timing sleeps or application data.
	if _, err := store.Conversations.UpdateOne(ctx, bson.M{"conversation_id": "conv"}, bson.M{"$set": bson.M{"execution_lease_until": time.Now().Add(-time.Second).UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimConversationExecution(ctx, "alice", "conv", "new", time.Minute); !ok || err != nil {
		t.Fatalf("takeover: %v %v", ok, err)
	}
	if err := store.ReleaseConversationExecution(ctx, "alice", "conv", "old"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.RenewConversationExecution(ctx, "alice", "conv", "old", time.Minute); ok || err != nil {
		t.Fatalf("old lease renewed successor: %v %v", ok, err)
	}
	if ok, err := store.RenewConversationExecution(ctx, "bob", "conv", "new", time.Minute); ok || err != nil {
		t.Fatalf("foreign owner renewed lease: %v %v", ok, err)
	}
	var state struct {
		ID string `bson:"execution_lease_id"`
	}
	if err := store.Conversations.FindOne(ctx, bson.M{"conversation_id": "conv"}).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.ID != "new" {
		t.Fatalf("stale release changed successor: %q", state.ID)
	}
	if err := store.ReleaseConversationExecution(ctx, "alice", "conv", "new"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimConversationExecution(ctx, "alice", "conv", "next", time.Minute); !ok || err != nil {
		t.Fatalf("released lease not reusable: %v %v", ok, err)
	}
}
