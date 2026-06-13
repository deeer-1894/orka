package db

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrNotFound is returned when a document is absent.
var ErrNotFound = errors.New("db: document not found")

// Storage wraps a Mongo database and its collections.
type Storage struct {
	client        *mongo.Client
	db            *mongo.Database
	Conversations *mongo.Collection
	Messages      *mongo.Collection
	Tasks         *mongo.Collection
	Users         *mongo.Collection
	Runs          *mongo.Collection
	Connectors    *mongo.Collection
}

// NewStorage connects to Mongo and pings it.
func NewStorage(ctx context.Context, uri, dbName string) (*Storage, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Decode interface{} (Meta/Payload) fields into bson.M (objects) instead of
	// the default bson.D (which JSON-marshals as a [{Key,Value}] array the
	// frontend can't read as msg.meta.agent_id / payload.tool).
	reg := bson.NewRegistry()
	reg.RegisterTypeMapEntry(bson.TypeEmbeddedDocument, reflect.TypeFor[bson.M]())
	cli, err := mongo.Connect(cctx, options.Client().ApplyURI(uri).SetRegistry(reg))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := cli.Ping(cctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	d := cli.Database(dbName)
	s := &Storage{
		client:        cli,
		db:            d,
		Conversations: d.Collection("conversations"),
		Messages:      d.Collection("messages"),
		Tasks:         d.Collection("tasks"),
		Users:         d.Collection("users"),
		Runs:          d.Collection("runs"),
		Connectors:    d.Collection("connectors"),
	}
	if err := s.EnsureIndexes(cctx); err != nil {
		return nil, fmt.Errorf("mongo indexes: %w", err)
	}
	return s, nil
}

// EnsureIndexes creates the indexes backing every hot query path so reads scale
// past a full-collection scan + in-memory sort. Idempotent (safe to re-run).
func (s *Storage) EnsureIndexes(ctx context.Context) error {
	specs := []struct {
		col   *mongo.Collection
		model mongo.IndexModel
	}{
		// users: unique email (also enforces no duplicate accounts).
		{s.Users, mongo.IndexModel{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("email_unique"),
		}},
		// messages: list a conversation's history newest-first.
		{s.Messages, mongo.IndexModel{
			Keys: bson.D{{Key: "conversation_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("conv_created"),
		}},
		// tasks: a user's tasks newest-first.
		{s.Tasks, mongo.IndexModel{
			Keys: bson.D{{Key: "owner_email", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		}},
		{s.Tasks, mongo.IndexModel{
			Keys: bson.D{{Key: "task_id", Value: 1}},
			Options: options.Index().SetName("task_id"),
		}},
		// runs: a user's run history newest-first, plus lookup by id.
		{s.Runs, mongo.IndexModel{
			Keys:    bson.D{{Key: "owner_email", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		}},
		{s.Runs, mongo.IndexModel{
			Keys:    bson.D{{Key: "run_id", Value: 1}},
			Options: options.Index().SetName("run_id"),
		}},
		{s.Connectors, mongo.IndexModel{
			Keys:    bson.D{{Key: "owner_email", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		}},
		// conversations: a user's conversations newest-first.
		{s.Conversations, mongo.IndexModel{
			Keys: bson.D{{Key: "owner_email", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		}},
		{s.Conversations, mongo.IndexModel{
			Keys: bson.D{{Key: "conversation_id", Value: 1}},
			Options: options.Index().SetName("conversation_id"),
		}},
	}
	for _, sp := range specs {
		if _, err := sp.col.Indexes().CreateOne(ctx, sp.model); err != nil {
			return err
		}
	}
	return nil
}

// ---- users ----

func (s *Storage) CreateUser(ctx context.Context, u *User) error {
	if _, err := s.Users.InsertOne(ctx, u); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Storage) GetUser(ctx context.Context, email string) (*User, error) {
	var out User
	err := s.Users.FindOne(ctx, bson.M{"email": email}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &out, nil
}

// ListConversationsByOwner returns a user's conversations, newest first.
func (s *Storage) ListConversationsByOwner(ctx context.Context, owner string, page, size int64) ([]ConversationTable, error) {
	var out []ConversationTable
	if err := paginate(ctx, s.Conversations, bson.M{"owner_email": owner}, page, size, &out); err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return out, nil
}

// Close disconnects the client.
func (s *Storage) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

// ---- conversations ----

func (s *Storage) CreateConversation(ctx context.Context, c *ConversationTable) error {
	if c.TaskIds == nil {
		c.TaskIds = []string{}
	}
	if _, err := s.Conversations.InsertOne(ctx, c); err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

func (s *Storage) GetConversation(ctx context.Context, id string) (*ConversationTable, error) {
	var out ConversationTable
	err := s.Conversations.FindOne(ctx, bson.M{"conversation_id": id}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	return &out, nil
}

func (s *Storage) ListConversations(ctx context.Context, page, size int64) ([]ConversationTable, error) {
	var out []ConversationTable
	if err := paginate(ctx, s.Conversations, bson.M{}, page, size, &out); err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return out, nil
}

func (s *Storage) UpdateConversationTitle(ctx context.Context, id, title string) error {
	_, err := s.Conversations.UpdateOne(ctx,
		bson.M{"conversation_id": id},
		bson.M{"$set": bson.M{"title": title}},
	)
	if err != nil {
		return fmt.Errorf("update conversation title: %w", err)
	}
	return nil
}

// DeleteConversation removes a conversation and its messages + tasks.
func (s *Storage) DeleteConversation(ctx context.Context, id string) error {
	if _, err := s.Conversations.DeleteOne(ctx, bson.M{"conversation_id": id}); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	_, _ = s.Messages.DeleteMany(ctx, bson.M{"conversation_id": id})
	_, _ = s.Tasks.DeleteMany(ctx, bson.M{"conversation_id": id})
	return nil
}

// PruneEmptyConversations deletes the owner's conversations that have no chat
// messages (the unused "New chat" placeholders). Returns the number removed.
func (s *Storage) PruneEmptyConversations(ctx context.Context, owner string) (int, error) {
	convs, err := s.ListConversationsByOwner(ctx, owner, 0, 10000)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(convs))
	for _, c := range convs {
		ids = append(ids, c.ConversationID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	// One query: which of those conversations actually have messages.
	vals, err := s.Messages.Distinct(ctx, "conversation_id", bson.M{"conversation_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, fmt.Errorf("distinct messages: %w", err)
	}
	nonEmpty := make(map[string]bool, len(vals))
	for _, v := range vals {
		if id, ok := v.(string); ok {
			nonEmpty[id] = true
		}
	}
	n := 0
	for _, id := range ids {
		if !nonEmpty[id] {
			if err := s.DeleteConversation(ctx, id); err == nil {
				n++
			}
		}
	}
	return n, nil
}

func (s *Storage) AddTaskToConversation(ctx context.Context, convID, taskID string) error {
	_, err := s.Conversations.UpdateOne(ctx,
		bson.M{"conversation_id": convID},
		bson.M{"$addToSet": bson.M{"task_ids": taskID}},
	)
	if err != nil {
		return fmt.Errorf("add task to conversation: %w", err)
	}
	return nil
}

// ---- messages ----

func (s *Storage) InsertMessage(ctx context.Context, m *MessagesTable) error {
	if _, err := s.Messages.InsertOne(ctx, m); err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (s *Storage) GetMessages(ctx context.Context, convID string, page, size int64) ([]MessagesTable, error) {
	var out []MessagesTable
	if err := paginate(ctx, s.Messages, bson.M{"conversation_id": convID}, page, size, &out); err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	return out, nil
}

// ---- tasks ----

func (s *Storage) CreateTask(ctx context.Context, t *TaskMeta) error {
	if _, err := s.Tasks.InsertOne(ctx, t); err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (s *Storage) GetTask(ctx context.Context, id string) (*TaskMeta, error) {
	var out TaskMeta
	err := s.Tasks.FindOne(ctx, bson.M{"task_id": id}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &out, nil
}

func (s *Storage) ListTasks(ctx context.Context, filter bson.M, page, size int64) ([]TaskMeta, error) {
	if filter == nil {
		filter = bson.M{}
	}
	var out []TaskMeta
	if err := paginate(ctx, s.Tasks, filter, page, size, &out); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

func (s *Storage) UpdateTaskStatus(ctx context.Context, id, runStatus string) error {
	_, err := s.Tasks.UpdateOne(ctx,
		bson.M{"task_id": id},
		bson.M{"$set": bson.M{"run_status": runStatus}},
	)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

// SetTaskWebhook sets (or clears, when token=="") a task's webhook trigger token.
func (s *Storage) SetTaskWebhook(ctx context.Context, id, token string) error {
	_, err := s.Tasks.UpdateOne(ctx, bson.M{"task_id": id}, bson.M{"$set": bson.M{"webhook_token": token}})
	if err != nil {
		return fmt.Errorf("set task webhook: %w", err)
	}
	return nil
}

// GetTaskByWebhook resolves a task from its webhook token (the token IS the auth).
func (s *Storage) GetTaskByWebhook(ctx context.Context, token string) (*TaskMeta, error) {
	var out TaskMeta
	err := s.Tasks.FindOne(ctx, bson.M{"webhook_token": token}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task by webhook: %w", err)
	}
	return &out, nil
}

// SetTaskCron turns recurring scheduling on/off for a task and sets its period
// and next-due time.
func (s *Storage) SetTaskCron(ctx context.Context, id, cronStatus string, intervalSec, nextRunAt int64) error {
	_, err := s.Tasks.UpdateOne(ctx,
		bson.M{"task_id": id},
		bson.M{"$set": bson.M{"cron_status": cronStatus, "interval_sec": intervalSec, "next_run_at": nextRunAt}},
	)
	if err != nil {
		return fmt.Errorf("set task cron: %w", err)
	}
	return nil
}

// AdvanceTaskRun records a cron run: pushes next_run_at forward and stores a
// short result summary.
func (s *Storage) AdvanceTaskRun(ctx context.Context, id string, nextRunAt int64, lastResult string) error {
	_, err := s.Tasks.UpdateOne(ctx,
		bson.M{"task_id": id},
		bson.M{"$set": bson.M{"next_run_at": nextRunAt, "last_result": lastResult, "run_status": RunRunning}},
	)
	if err != nil {
		return fmt.Errorf("advance task run: %w", err)
	}
	return nil
}

// CreateRun records the start of an execution.
func (s *Storage) CreateRun(ctx context.Context, r *RunRecord) error {
	if _, err := s.Runs.InsertOne(ctx, r); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// FinalizeRun stamps the terminal state of a run. Called with a background
// context since it runs after the request context may be cancelled.
func (s *Storage) FinalizeRun(ctx context.Context, r RunRecord) error {
	set := bson.M{
		"status": r.Status, "output": r.Output, "error": r.Error,
		"tokens": r.Tokens, "tool_calls": r.ToolCalls,
		"finished_at": r.FinishedAt, "duration_ms": r.DurationMs,
	}
	_, err := s.Runs.UpdateOne(ctx, bson.M{"run_id": r.RunID}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("finalize run: %w", err)
	}
	return nil
}

// ListRuns returns runs matching the filter, newest-first.
func (s *Storage) ListRuns(ctx context.Context, filter bson.M, page, size int64) ([]RunRecord, error) {
	if filter == nil {
		filter = bson.M{}
	}
	var out []RunRecord
	if err := paginate(ctx, s.Runs, filter, page, size, &out); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return out, nil
}

// GetRun fetches one run by id.
func (s *Storage) GetRun(ctx context.Context, runID string) (*RunRecord, error) {
	var out RunRecord
	err := s.Runs.FindOne(ctx, bson.M{"run_id": runID}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return &out, nil
}

// --- connectors (user-registered external MCP servers) ---

func (s *Storage) CreateConnector(ctx context.Context, c *Connector) error {
	if _, err := s.Connectors.InsertOne(ctx, c); err != nil {
		return fmt.Errorf("insert connector: %w", err)
	}
	return nil
}

func (s *Storage) ListConnectors(ctx context.Context, owner string) ([]Connector, error) {
	var out []Connector
	if err := paginate(ctx, s.Connectors, bson.M{"owner_email": owner}, 0, 100, &out); err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	return out, nil
}

// EnabledConnectors returns a user's enabled connectors (for the runtime merge).
func (s *Storage) EnabledConnectors(ctx context.Context, owner string) ([]Connector, error) {
	var out []Connector
	if err := paginate(ctx, s.Connectors, bson.M{"owner_email": owner, "enabled": true}, 0, 100, &out); err != nil {
		return nil, fmt.Errorf("enabled connectors: %w", err)
	}
	return out, nil
}

func (s *Storage) DeleteConnector(ctx context.Context, id, owner string) error {
	_, err := s.Connectors.DeleteOne(ctx, bson.M{"connector_id": id, "owner_email": owner})
	if err != nil {
		return fmt.Errorf("delete connector: %w", err)
	}
	return nil
}

// paginate is a generic find-with-skip/limit helper, newest first by created_at.
func paginate[T any](ctx context.Context, col *mongo.Collection, filter bson.M, page, size int64, out *[]T) error {
	if size <= 0 {
		size = 20
	}
	if page < 0 {
		page = 0
	}
	opts := options.Find().
		SetSkip(page * size).
		SetLimit(size).
		SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	dst := make([]T, 0)
	if err := cur.All(ctx, &dst); err != nil {
		return err
	}
	*out = dst
	return nil
}
