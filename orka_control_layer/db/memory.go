package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Personalization is what a user tells Orka about themselves once instead of
// in every conversation. AutoMemory additionally lets runs save facts they
// learn (off until the user turns it on).
type Personalization struct {
	AboutMe    string `bson:"about_me,omitempty" json:"about_me"`
	ReplyStyle string `bson:"reply_style,omitempty" json:"reply_style"`
	AutoMemory bool   `bson:"auto_memory,omitempty" json:"auto_memory"`
}

const (
	MaxPersonalText  = 1500
	MaxMemoryRunes   = 300
	MaxMemoriesKept  = 200
	MemoriesInPrompt = 20
)

func (p Personalization) Normalize() (Personalization, error) {
	p.AboutMe, p.ReplyStyle = strings.TrimSpace(p.AboutMe), strings.TrimSpace(p.ReplyStyle)
	if utf8.RuneCountInString(p.AboutMe) > MaxPersonalText || utf8.RuneCountInString(p.ReplyStyle) > MaxPersonalText {
		return p, fmt.Errorf("每项不超过 %d 字", MaxPersonalText)
	}
	return p, nil
}

// Memory is one fact a run saved about the user, optionally only for one project.
type Memory struct {
	MemoryID             string `bson:"memory_id" json:"memory_id"`
	OwnerEmail           string `bson:"owner_email" json:"owner_email"`
	ProjectID            string `bson:"project_id,omitempty" json:"project_id,omitempty"`
	Content              string `bson:"content" json:"content"`
	SourceConversationID string `bson:"source_conversation_id,omitempty" json:"source_conversation_id,omitempty"`
	CreatedAt            int64  `bson:"created_at" json:"created_at"`
}

// sensitive matches what must never be stored as a memory: credentials,
// identity and payment numbers. The tool description forbids them too; this
// is the check that does not depend on the model listening.
var sensitive = regexp.MustCompile(`(?i)(密码|口令|password|passwd|身份证|护照|银行卡|信用卡|卡号|cvv|api[ _-]?key|secret|token|私钥|sk-[a-z0-9]{12,}|\d{12,})`)

// ErrSensitiveMemory rejects a memory that looks like a secret or an ID number.
var ErrSensitiveMemory = errors.New("记忆里不能保存密码、证件号、银行卡号或密钥")

// NormalizeMemory trims a memory and rejects empty, oversized or sensitive ones.
func NormalizeMemory(content string) (string, error) {
	content = strings.TrimSpace(content)
	switch {
	case content == "":
		return "", errors.New("记忆内容为空")
	case utf8.RuneCountInString(content) > MaxMemoryRunes:
		return "", fmt.Errorf("一条记忆不超过 %d 字", MaxMemoryRunes)
	case sensitive.MatchString(strings.ReplaceAll(content, " ", "")):
		return "", ErrSensitiveMemory
	}
	return content, nil
}

func (s *Storage) GetPersonalization(ctx context.Context, email string) (Personalization, error) {
	var out struct {
		Personal Personalization `bson:"personal"`
	}
	err := s.Users.FindOne(ctx, bson.M{"email": email}, options.FindOne().SetProjection(bson.M{"personal": 1})).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Personalization{}, ErrNotFound
	}
	if err != nil {
		return Personalization{}, fmt.Errorf("get personalization: %w", err)
	}
	return out.Personal, nil
}

func (s *Storage) SavePersonalization(ctx context.Context, email string, p Personalization) error {
	res, err := s.Users.UpdateOne(ctx, bson.M{"email": email}, bson.M{"$set": bson.M{"personal": p}})
	if err != nil {
		return fmt.Errorf("save personalization: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMemory stores a memory, skipping an exact repeat, and keeps only the
// newest MaxMemoriesKept per owner.
func (s *Storage) AddMemory(ctx context.Context, m *Memory) (bool, error) {
	var project any = m.ProjectID
	if m.ProjectID == "" {
		project = bson.M{"$in": bson.A{nil, ""}} // general memories omit the field
	}
	n, err := s.Memories.CountDocuments(ctx, bson.M{"owner_email": m.OwnerEmail, "project_id": project, "content": m.Content})
	if err != nil {
		return false, fmt.Errorf("add memory: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	if _, err := s.Memories.InsertOne(ctx, m); err != nil {
		return false, fmt.Errorf("add memory: %w", err)
	}
	old, err := s.ListMemories(ctx, m.OwnerEmail, MaxMemoriesKept+50)
	if err == nil && len(old) > MaxMemoriesKept {
		ids := make([]string, 0, len(old)-MaxMemoriesKept)
		for _, x := range old[MaxMemoriesKept:] {
			ids = append(ids, x.MemoryID)
		}
		_, _ = s.Memories.DeleteMany(ctx, bson.M{"owner_email": m.OwnerEmail, "memory_id": bson.M{"$in": ids}})
	}
	return true, nil
}

// ListMemories returns the owner's memories, newest first.
func (s *Storage) ListMemories(ctx context.Context, owner string, limit int64) ([]Memory, error) {
	cur, err := s.Memories.Find(ctx, bson.M{"owner_email": owner}, options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	out := []Memory{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	return out, nil
}

// MemoriesFor are the newest memories a run may read: the user's general
// ones plus those of the run's project.
func (s *Storage) MemoriesFor(ctx context.Context, owner, projectID string) ([]Memory, error) {
	scope := []bson.M{{"project_id": bson.M{"$exists": false}}, {"project_id": ""}}
	if projectID != "" {
		scope = append(scope, bson.M{"project_id": projectID})
	}
	cur, err := s.Memories.Find(ctx, bson.M{"owner_email": owner, "$or": scope}, options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(MemoriesInPrompt))
	if err != nil {
		return nil, fmt.Errorf("memories for run: %w", err)
	}
	out := []Memory{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("memories for run: %w", err)
	}
	return out, nil
}

// DeleteMemory removes one of the owner's memories for good.
func (s *Storage) DeleteMemory(ctx context.Context, owner, id string) error {
	res, err := s.Memories.DeleteOne(ctx, bson.M{"owner_email": owner, "memory_id": id})
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
