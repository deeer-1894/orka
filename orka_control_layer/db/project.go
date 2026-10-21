package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Project groups an owner's conversations around one piece of ongoing work:
// standing instructions every run in it reads, and files every conversation
// in it shares (stored beside the sessions, see pathsafe.ProjectRoot).
type Project struct {
	ProjectID    string `bson:"project_id" json:"project_id"`
	OwnerEmail   string `bson:"owner_email" json:"owner_email"`
	Name         string `bson:"name" json:"name"`
	Description  string `bson:"description,omitempty" json:"description,omitempty"`
	Instructions string `bson:"instructions,omitempty" json:"instructions,omitempty"`
	// DefaultSkill is the skill a run in the project uses when the user locked none.
	DefaultSkill string `bson:"default_skill,omitempty" json:"default_skill,omitempty"`
	CreatedAt    int64  `bson:"created_at" json:"created_at"`
	UpdatedAt    int64  `bson:"updated_at" json:"updated_at"`
}

const (
	maxProjectName         = 60
	maxProjectDescription  = 300
	MaxProjectInstructions = 8000
)

// Normalize trims the editable fields and enforces their limits.
func (p Project) Normalize() (Project, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Instructions = strings.TrimSpace(p.Instructions)
	p.DefaultSkill = strings.TrimSpace(p.DefaultSkill)
	switch {
	case p.Name == "":
		return p, errors.New("项目名称不能为空")
	case utf8.RuneCountInString(p.Name) > maxProjectName:
		return p, fmt.Errorf("项目名称不超过 %d 字", maxProjectName)
	case utf8.RuneCountInString(p.Description) > maxProjectDescription:
		return p, fmt.Errorf("项目说明不超过 %d 字", maxProjectDescription)
	case utf8.RuneCountInString(p.Instructions) > MaxProjectInstructions:
		return p, fmt.Errorf("项目指令不超过 %d 字", MaxProjectInstructions)
	}
	return p, nil
}

func (s *Storage) CreateProject(ctx context.Context, p *Project) error {
	if _, err := s.Projects.InsertOne(ctx, p); err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	return nil
}

// GetProject returns the owner's project; another owner's is ErrNotFound.
func (s *Storage) GetProject(ctx context.Context, owner, id string) (*Project, error) {
	var out Project
	err := s.Projects.FindOne(ctx, bson.M{"project_id": id, "owner_email": owner}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &out, nil
}

func (s *Storage) ListProjects(ctx context.Context, owner string) ([]Project, error) {
	cur, err := s.Projects.Find(ctx, bson.M{"owner_email": owner}, options.Find().SetSort(bson.M{"updated_at": -1}).SetLimit(200))
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := []Project{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return out, nil
}

func (s *Storage) UpdateProject(ctx context.Context, p *Project) error {
	res, err := s.Projects.UpdateOne(ctx, bson.M{"project_id": p.ProjectID, "owner_email": p.OwnerEmail}, bson.M{"$set": bson.M{
		"name": p.Name, "description": p.Description, "instructions": p.Instructions, "default_skill": p.DefaultSkill, "updated_at": p.UpdatedAt,
	}})
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProject removes the project record and returns its conversations to
// the owner's general list. Their files are untouched.
func (s *Storage) DeleteProject(ctx context.Context, owner, id string) error {
	res, err := s.Projects.DeleteOne(ctx, bson.M{"project_id": id, "owner_email": owner})
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	if _, err := s.Conversations.UpdateMany(ctx, bson.M{"project_id": id, "owner_email": owner}, bson.M{"$unset": bson.M{"project_id": ""}}); err != nil {
		return fmt.Errorf("release project conversations: %w", err)
	}
	return nil
}

// MoveConversation files the owner's conversation under projectID, or takes
// it out of any project when projectID is "".
func (s *Storage) MoveConversation(ctx context.Context, owner, conversationID, projectID string) error {
	update := bson.M{"$unset": bson.M{"project_id": ""}}
	if projectID != "" {
		update = bson.M{"$set": bson.M{"project_id": projectID}}
	}
	res, err := s.Conversations.UpdateOne(ctx, bson.M{"conversation_id": conversationID, "owner_email": owner}, update)
	if err != nil {
		return fmt.Errorf("move conversation: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}
