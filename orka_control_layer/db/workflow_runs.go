package db

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// WorkflowRun is an atomic snapshot of the parent and all its steps. Step names
// are values (not Mongo field paths), so names containing dots remain safe.
type WorkflowRun struct {
	RunID          string            `bson:"_id" json:"run_id"`
	WorkflowID     string            `bson:"workflow_id" json:"workflow_id"`
	OwnerEmail     string            `bson:"owner_email" json:"owner_email"`
	ConversationID string            `bson:"conversation_id" json:"conversation_id"`
	Status         string            `bson:"status" json:"status"`
	Error          string            `bson:"error,omitempty" json:"error,omitempty"`
	Definition     []WorkflowStep    `bson:"definition" json:"definition"`
	Steps          []WorkflowStepRun `bson:"steps" json:"steps"`
	CreatedAt      int64             `bson:"created_at" json:"created_at"`
	FinishedAt     int64             `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
}

type WorkflowStepRun struct {
	Name        string `bson:"name" json:"name"`
	ExecutionID string `bson:"execution_id,omitempty" json:"execution_id,omitempty"`
	Status      string `bson:"status" json:"status"`
	Output      string `bson:"output,omitempty" json:"output,omitempty"`
	Error       string `bson:"error,omitempty" json:"error,omitempty"`
	Attempts    int    `bson:"attempts" json:"attempts"`
	StartedAt   int64  `bson:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt  int64  `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
}

func (s *Storage) SaveWorkflowRun(ctx context.Context, run WorkflowRun) error {
	if run.RunID == "" {
		return errors.New("workflow run ID required")
	}
	_, err := s.Workflows.Database().Collection("workflow_runs").ReplaceOne(ctx, bson.M{"_id": run.RunID}, run, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("save workflow run: %w", err)
	}
	return nil
}

func (s *Storage) GetWorkflowRun(ctx context.Context, id, owner string) (*WorkflowRun, error) {
	var r WorkflowRun
	err := s.Workflows.Database().Collection("workflow_runs").FindOne(ctx, bson.M{"_id": id, "owner_email": owner}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow run: %w", err)
	}
	return &r, nil
}

// EnsureWorkflowConversation creates the conversation before its execution lease
// is acquired. A deterministic _id prevents two workflow starters inserting
// duplicate conversation documents even before a unique legacy index exists.
func (s *Storage) EnsureWorkflowConversation(ctx context.Context, c ConversationTable) error {
	if c.ConversationID == "" || c.OwnerEmail == "" {
		return errors.New("workflow conversation and owner required")
	}
	filter := bson.M{"conversation_id": c.ConversationID, "owner_email": c.OwnerEmail}
	insert := bson.M{"_id": "workflow:" + c.ConversationID, "conversation_id": c.ConversationID, "owner_email": c.OwnerEmail, "title": c.Title, "task_ids": c.TaskIds, "created_at": c.CreatedAt}
	_, err := s.Conversations.UpdateOne(ctx, filter, bson.M{"$setOnInsert": insert}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		// Another starter inserted the same workflow conversation. Verify owner;
		// never interpret a foreign owner's ID collision as authorization.
		return s.Conversations.FindOne(ctx, filter).Err()
	}
	return err
}
