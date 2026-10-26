package db

import (
	"context"
	"testing"
)

func TestRetentionQueries(t *testing.T) {
	s := isolatedWorkflowDB(t)
	d := s.Tasks.Database()
	s.ArtifactVers, s.Conversations, s.Runs, s.Notifications = d.Collection("artifact_versions"), d.Collection("conversations"), d.Collection("runs"), d.Collection("notifications")
	ctx := context.Background()

	for v := 1; v <= 25; v++ {
		s.ArtifactVers.InsertOne(ctx, ArtifactVersion{ArtifactID: "a", Version: v})
	}
	s.ArtifactVers.InsertOne(ctx, ArtifactVersion{ArtifactID: "b", Version: 1})
	s.Artifacts = d.Collection("artifacts")
	s.Artifacts.InsertOne(ctx, Artifact{ArtifactID: "a", CurrentVersion: 2}) // rolled back
	if n, err := s.PruneArtifactVersions(ctx, 20); err != nil || n != 4 {
		t.Fatalf("pruned %d (%v), want 4", n, err)
	}
	if n, _ := s.ArtifactVers.CountDocuments(ctx, map[string]any{"artifact_id": "a", "version": map[string]any{"$gte": 6}}); n != 20 {
		t.Fatalf("kept %d of the newest versions, want 20", n)
	}
	if n, _ := s.ArtifactVers.CountDocuments(ctx, map[string]any{"artifact_id": "a", "version": 2}); n != 1 {
		t.Fatal("the version an artifact points at must survive")
	}

	s.Messages = d.Collection("messages")
	s.Conversations.InsertOne(ctx, ConversationTable{ConversationID: "old", CreatedAt: 1})
	s.Conversations.InsertOne(ctx, ConversationTable{ConversationID: "filed", CreatedAt: 1, ProjectID: "p"})
	s.Conversations.InsertOne(ctx, ConversationTable{ConversationID: "scheduled", CreatedAt: 1})
	s.Conversations.InsertOne(ctx, ConversationTable{ConversationID: "still-used", CreatedAt: 1})
	s.Conversations.InsertOne(ctx, ConversationTable{ConversationID: "recent", CreatedAt: 100})
	s.Messages.InsertOne(ctx, MessagesTable{ConversationID: "still-used", CreatedAt: 90})
	s.Messages.InsertOne(ctx, MessagesTable{ConversationID: "old", CreatedAt: 2})
	s.Tasks.InsertOne(ctx, TaskMeta{TaskID: "t", ConversationID: "scheduled"})
	stale, err := s.StaleConversations(ctx, 50, 10)
	if err != nil || len(stale) != 1 || stale[0].ConversationID != "old" {
		t.Fatalf("stale = %+v (%v), want only the unfiled, unscheduled old one", stale, err)
	}

	s.Runs.InsertOne(ctx, RunRecord{RunID: "done", Status: "done", CreatedAt: 1})
	s.Runs.InsertOne(ctx, RunRecord{RunID: "paused", Status: "paused", CreatedAt: 1})
	if n, _ := s.DeleteFinishedRunsBefore(ctx, 50); n != 1 {
		t.Fatalf("deleted %d runs, want only the finished one", n)
	}
	s.Notifications.InsertOne(ctx, Notification{NotificationID: "n1", CreatedAt: 1})
	s.Notifications.InsertOne(ctx, Notification{NotificationID: "n2", CreatedAt: 100})
	if n, _ := s.DeleteNotificationsBefore(ctx, 50); n != 1 {
		t.Fatalf("deleted %d notifications, want 1", n)
	}
}
