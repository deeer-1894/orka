package db

import (
	"context"
	"errors"
	"testing"
)

func TestProjectsBelongToTheirOwnerAndReleaseConversationsOnDelete(t *testing.T) {
	base := isolatedWorkflowDB(t)
	d := base.Tasks.Database()
	store := &Storage{Projects: d.Collection("projects"), Conversations: d.Collection("conversations")}
	ctx := context.Background()
	p := &Project{ProjectID: "prj_1", OwnerEmail: "a@x", Name: "竞品研究", CreatedAt: 1, UpdatedAt: 1}
	if err := store.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProject(ctx, "b@x", "prj_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner must not see the project, err=%v", err)
	}
	for _, id := range []string{"c1", "c2"} {
		if err := store.CreateConversation(ctx, &ConversationTable{ConversationID: id, OwnerEmail: "a@x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MoveConversation(ctx, "b@x", "c1", "prj_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("only the owner moves a conversation, err=%v", err)
	}
	if err := store.MoveConversation(ctx, "a@x", "c1", "prj_1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.GetConversation(ctx, "c1"); c.ProjectID != "prj_1" {
		t.Fatalf("c1 project = %q", c.ProjectID)
	}
	p.Name, p.Instructions, p.UpdatedAt = "竞品", "用表格", 2
	if err := store.UpdateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListProjects(ctx, "a@x"); len(list) != 1 || list[0].Instructions != "用表格" {
		t.Fatalf("list = %+v", list)
	}
	if err := store.DeleteProject(ctx, "a@x", "prj_1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.GetConversation(ctx, "c1"); c.ProjectID != "" {
		t.Fatal("deleting a project returns its conversations to the general list")
	}
}

func TestProjectNormalizeEnforcesLimits(t *testing.T) {
	if _, err := (Project{Name: "  "}).Normalize(); err == nil {
		t.Fatal("a project needs a name")
	}
	long := make([]rune, MaxProjectInstructions+1)
	for i := range long {
		long[i] = '字'
	}
	if _, err := (Project{Name: "x", Instructions: string(long)}).Normalize(); err == nil {
		t.Fatal("instructions are bounded")
	}
}
