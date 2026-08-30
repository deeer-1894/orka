package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	Notifications *mongo.Collection
	Workflows     *mongo.Collection
	Artifacts     *mongo.Collection
	ArtifactVers  *mongo.Collection
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
		Notifications: d.Collection("notifications"),
		Workflows:     d.Collection("workflows"),
		Artifacts:     d.Collection("artifacts"),
		ArtifactVers:  d.Collection("artifact_versions"),
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
		{s.Notifications, mongo.IndexModel{
			Keys:    bson.D{{Key: "owner_email", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("owner_created"),
		}},
		{s.Workflows, mongo.IndexModel{
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
		{s.Artifacts, mongo.IndexModel{
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("slug_unique"),
		}},
		{s.Artifacts, mongo.IndexModel{
			Keys:    bson.D{{Key: "owner_email", Value: 1}, {Key: "updated_at", Value: -1}},
			Options: options.Index().SetName("owner_updated"),
		}},
		{s.Artifacts, mongo.IndexModel{
			Keys:    bson.D{{Key: "conversation_id", Value: 1}},
			Options: options.Index().SetName("artifact_conversation"),
		}},
		{s.ArtifactVers, mongo.IndexModel{
			Keys:    bson.D{{Key: "artifact_id", Value: 1}, {Key: "version", Value: -1}},
			Options: options.Index().SetUnique(true).SetName("artifact_version"),
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

// UpdateConversationShares replaces a conversation's share list.
func (s *Storage) UpdateConversationShares(ctx context.Context, id string, shares []ConversationShare) error {
	if shares == nil {
		shares = []ConversationShare{}
	}
	_, err := s.Conversations.UpdateOne(ctx,
		bson.M{"conversation_id": id},
		bson.M{"$set": bson.M{"shares": shares}},
	)
	if err != nil {
		return fmt.Errorf("update conversation shares: %w", err)
	}
	return nil
}

// ListSharedWith returns conversations shared with email (not owned by them),
// newest first.
func (s *Storage) ListSharedWith(ctx context.Context, email string, page, size int64) ([]ConversationTable, error) {
	var out []ConversationTable
	filter := bson.M{"shares.email": email, "owner_email": bson.M{"$ne": email}}
	if err := paginate(ctx, s.Conversations, filter, page, size, &out); err != nil {
		return nil, fmt.Errorf("list shared conversations: %w", err)
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

// GetChatTurns returns the newest `size` USER/ASSISTANT turns of a conversation.
//
// Filtering in the query is the whole point. Fetching a fixed window and then
// discarding non-chat rows in Go sounds equivalent and is not: a conversation is
// mostly tool traffic, so the window fills with rows that are about to be thrown
// away. Measured on real conversations, the newest 60 rows yielded 7, 5 and in
// one case 1 usable turn — the harder a run worked, the less of it the next turn
// could see.
func (s *Storage) GetChatTurns(ctx context.Context, convID string, size int64) ([]MessagesTable, error) {
	// Literals rather than the messages constants: this package deliberately
	// carries no orka_core dependency (see randID), and these are the stored
	// wire values, not the Go types.
	filter := bson.M{
		"conversation_id": convID,
		"type":            "chat",
		"role":            bson.M{"$in": []string{"user", "assistant"}},
	}
	var out []MessagesTable
	if err := paginate(ctx, s.Messages, filter, 0, size, &out); err != nil {
		return nil, fmt.Errorf("get chat turns: %w", err)
	}
	return out, nil
}

// AppendRunDigest records what a run did onto its conversation, keeping only the
// most recent entries so a long-lived conversation cannot grow an unbounded
// preamble — the memory has to stay smaller than the thing it replaces.
func (s *Storage) AppendRunDigest(ctx context.Context, convID string, d RunDigest, keep int) error {
	if convID == "" {
		return nil
	}
	_, err := s.Conversations.UpdateOne(ctx, bson.M{"conversation_id": convID},
		bson.M{"$push": bson.M{"digests": bson.M{
			"$each":  []RunDigest{d},
			"$slice": -keep,
		}}})
	if err != nil {
		return fmt.Errorf("append run digest: %w", err)
	}
	return nil
}

// GetRunDigests returns a conversation's run digests, oldest first.
func (s *Storage) GetRunDigests(ctx context.Context, convID string) ([]RunDigest, error) {
	var c ConversationTable
	err := s.Conversations.FindOne(ctx, bson.M{"conversation_id": convID},
		options.FindOne().SetProjection(bson.M{"digests": 1})).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get run digests: %w", err)
	}
	return c.Digests, nil
}

// readableConvIDs returns the conversation ids a user may read (owned + shared).
func (s *Storage) readableConvIDs(ctx context.Context, owner string) ([]string, map[string]string, error) {
	cur, err := s.Conversations.Find(ctx, bson.M{"$or": bson.A{
		bson.M{"owner_email": owner},
		bson.M{"shares.email": owner},
	}})
	if err != nil {
		return nil, nil, err
	}
	defer cur.Close(ctx)
	var convs []ConversationTable
	if err := cur.All(ctx, &convs); err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(convs))
	titles := make(map[string]string, len(convs))
	for _, c := range convs {
		ids = append(ids, c.ConversationID)
		titles[c.ConversationID] = c.Title
	}
	return ids, titles, nil
}

// SearchMessages finds chat/stream messages whose text contains query across all
// conversations the user can read — the backend half of cross-conversation
// full-text search. Returns matches newest-first with the owning title attached.
func (s *Storage) SearchMessages(ctx context.Context, owner, query string, limit int64) ([]MessageSearchHit, error) {
	q := query
	if len(q) > 200 {
		q = q[:200]
	}
	ids, titles, err := s.readableConvIDs(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("search: readable convs: %w", err)
	}
	if len(ids) == 0 {
		return []MessageSearchHit{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 30
	}
	rx := primitive.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
	filter := bson.M{
		"conversation_id": bson.M{"$in": ids},
		"type":            bson.M{"$in": bson.A{"chat", "stream"}},
		"role":            bson.M{"$in": bson.A{"user", "assistant"}},
		"content":         bson.M{"$regex": rx},
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cur, err := s.Messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer cur.Close(ctx)
	var msgs []MessagesTable
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, fmt.Errorf("search decode: %w", err)
	}
	hits := make([]MessageSearchHit, 0, len(msgs))
	for _, m := range msgs {
		hits = append(hits, MessageSearchHit{
			ConversationID: m.ConversationID,
			Title:          titles[m.ConversationID],
			Snippet:        snippetAround(m.Content, q),
			Role:           m.Role,
			CreatedAt:      m.CreatedAt,
		})
	}
	return hits, nil
}

// snippetAround returns a short window of text centered on the first occurrence
// of q (case-insensitive), with ellipses — enough to recognize the hit in a list.
func snippetAround(text, q string) string {
	const pad = 48
	runes := []rune(text)
	byteIdx := strings.Index(strings.ToLower(text), strings.ToLower(q))
	if byteIdx < 0 {
		if len(runes) > 2*pad {
			return string(runes[:2*pad]) + "…"
		}
		return text
	}
	idx := len([]rune(text[:byteIdx])) // byte offset → rune offset
	start := idx - pad
	if start < 0 {
		start = 0
	}
	end := idx + len([]rune(q)) + pad
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out = out + "…"
	}
	return out
}

// randID returns a short random hex id (mirrors orka_core/messages.NewID without
// importing it into the storage layer).
func randID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ForkConversation creates a branch of src: a new conversation owned by `owner`
// seeded with a copy of src's messages up to and including the cutoff message
// (by timestamp). The branch records its parent so the sidebar can nest it. The
// original is untouched, so the user can explore an alternative from that turn.
func (s *Storage) ForkConversation(ctx context.Context, srcID, uptoMessageID, owner string) (*ConversationTable, error) {
	src, err := s.GetConversation(ctx, srcID)
	if err != nil {
		return nil, err
	}
	// Find the cutoff timestamp from the named message (default: copy everything).
	cutoff := int64(1<<63 - 1)
	if uptoMessageID != "" {
		var cut MessagesTable
		if err := s.Messages.FindOne(ctx, bson.M{"_id": uptoMessageID}).Decode(&cut); err == nil {
			cutoff = cut.CreatedAt
		}
	}
	branch := &ConversationTable{
		ConversationID:       randID(),
		OwnerEmail:           owner,
		Title:                strings.TrimSpace(src.Title) + " · 分支",
		TaskIds:              []string{},
		CreatedAt:            time.Now().UnixMilli(),
		ParentConversationID: srcID,
	}
	if err := s.CreateConversation(ctx, branch); err != nil {
		return nil, err
	}
	// Copy messages up to the cutoff, in chronological order, with fresh ids.
	cur, err := s.Messages.Find(ctx,
		bson.M{"conversation_id": srcID, "created_at": bson.M{"$lte": cutoff}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	)
	if err != nil {
		return branch, nil // branch exists; copy failed — return what we have
	}
	defer cur.Close(ctx)
	var msgs []MessagesTable
	if err := cur.All(ctx, &msgs); err != nil {
		return branch, nil
	}
	docs := make([]any, 0, len(msgs))
	for _, m := range msgs {
		m.ID = randID()
		m.ConversationID = branch.ConversationID
		docs = append(docs, m)
	}
	if len(docs) > 0 {
		_, _ = s.Messages.InsertMany(ctx, docs)
	}
	return branch, nil
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
		"status": r.Status, "output": r.Output, "result": r.Result, "error": r.Error,
		"tokens": r.Tokens, "tool_calls": r.ToolCalls,
		"finished_at": r.FinishedAt, "duration_ms": r.DurationMs,
		"budget_hit": r.BudgetHit, "unfinished": r.Unfinished,
	}
	_, err := s.Runs.UpdateOne(ctx, bson.M{"run_id": r.RunID}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("finalize run: %w", err)
	}
	return nil
}

// TouchRun refreshes a run's heartbeat. Best-effort: a missed beat is harmless
// because the reaper's staleness window is many beats wide.
func (s *Storage) TouchRun(ctx context.Context, runID string) error {
	_, err := s.Runs.UpdateOne(ctx,
		bson.M{"run_id": runID, "status": RunRunning},
		bson.M{"$set": bson.M{"heartbeat_at": time.Now().UnixMilli()}},
	)
	return err
}

// ReapStaleRuns closes out runs that are still marked running but whose owning
// process is gone — the residue of a restart, crash or deploy. They are marked
// interrupted rather than failed: the work did not go wrong, it stopped
// existing, and only one of those is worth alerting on.
//
// Staleness is measured from the heartbeat, falling back to the start time for
// records written before heartbeats existed (their process is long gone too).
// Called once at startup and periodically after, so a run orphaned by a crash
// is cleared within one staleness window rather than lingering indefinitely.
// Returns the ids it closed out, so the caller can check each for a surviving
// transcript — a crashed run never got to flag itself resumable, and that is
// exactly the case where continuing is worth the most.
func (s *Storage) ReapStaleRuns(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	filter := bson.M{"status": RunRunning, "$or": []bson.M{
		{"heartbeat_at": bson.M{"$lt": cutoff}},
		{"heartbeat_at": bson.M{"$exists": false}, "created_at": bson.M{"$lt": cutoff}},
	}}
	// Collect the ids BEFORE the update, while the filter still matches them.
	cur, err := s.Runs.Find(ctx, filter, options.Find().SetProjection(bson.M{"run_id": 1}))
	if err != nil {
		return nil, fmt.Errorf("reap stale runs: %w", err)
	}
	var rows []struct {
		RunID string `bson:"run_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("reap stale runs: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if _, err := s.Runs.UpdateMany(ctx, filter, bson.M{"$set": bson.M{
		"status": RunInterrupted,
		"error":  "run interrupted — the serving process went away",
	}}); err != nil {
		return nil, fmt.Errorf("reap stale runs: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.RunID)
	}
	return ids, nil
}

// SetRunResumable flags a failed run as continuable and records how much of it
// the surviving transcript covers.
func (s *Storage) SetRunResumable(ctx context.Context, runID string, steps int) error {
	_, err := s.Runs.UpdateOne(ctx, bson.M{"run_id": runID},
		bson.M{"$set": bson.M{"resumable": true, "resume_steps": steps}})
	if err != nil {
		return fmt.Errorf("set run resumable: %w", err)
	}
	return nil
}

// ClearRunResumable drops the flag once a run's transcript has been consumed,
// so a run cannot be resumed twice from the same point.
func (s *Storage) ClearRunResumable(ctx context.Context, runID string) error {
	_, err := s.Runs.UpdateOne(ctx, bson.M{"run_id": runID},
		bson.M{"$set": bson.M{"resumable": false}})
	if err != nil {
		return fmt.Errorf("clear run resumable: %w", err)
	}
	return nil
}

// TokensSince totals a user's token spend since a point in time — the input to
// the per-user cost ceiling. Runs that never recorded usage contribute zero.
func (s *Storage) TokensSince(ctx context.Context, email string, since int64) (int, error) {
	cur, err := s.Runs.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"owner_email": email, "created_at": bson.M{"$gte": since}}},
		{"$group": bson.M{"_id": nil, "total": bson.M{"$sum": "$tokens"}}},
	})
	if err != nil {
		return 0, fmt.Errorf("tokens since: %w", err)
	}
	defer cur.Close(ctx)
	var rows []struct {
		Total int64 `bson:"total"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return 0, fmt.Errorf("tokens since: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return int(rows[0].Total), nil
}

// RecordTaskOutcome advances a scheduled task's circuit breaker. A success
// clears the strike count; a failure increments it and returns the new total so
// the caller can trip the breaker.
func (s *Storage) RecordTaskOutcome(ctx context.Context, taskID string, ok bool) (int, error) {
	if taskID == "" {
		return 0, nil
	}
	update := bson.M{"$inc": bson.M{"consecutive_fails": 1}}
	if ok {
		update = bson.M{"$set": bson.M{"consecutive_fails": 0}}
	}
	var out TaskMeta
	err := s.Tasks.FindOneAndUpdate(ctx, bson.M{"task_id": taskID}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("record task outcome: %w", err)
	}
	return out.ConsecutiveFails, nil
}

// DisableTask stops a scheduled task and records why, so an automatic shutdown
// is distinguishable from one the user performed.
func (s *Storage) DisableTask(ctx context.Context, taskID, reason string) error {
	_, err := s.Tasks.UpdateOne(ctx, bson.M{"task_id": taskID},
		bson.M{"$set": bson.M{"cron_status": "stopped", "next_run_at": int64(0), "disabled_reason": reason}})
	if err != nil {
		return fmt.Errorf("disable task: %w", err)
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

// --- workflows ---

func (s *Storage) CreateWorkflow(ctx context.Context, w *Workflow) error {
	if _, err := s.Workflows.InsertOne(ctx, w); err != nil {
		return fmt.Errorf("insert workflow: %w", err)
	}
	return nil
}

func (s *Storage) ListWorkflows(ctx context.Context, owner string) ([]Workflow, error) {
	var out []Workflow
	if err := paginate(ctx, s.Workflows, bson.M{"owner_email": owner}, 0, 100, &out); err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	return out, nil
}

func (s *Storage) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	var out Workflow
	err := s.Workflows.FindOne(ctx, bson.M{"workflow_id": id}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	return &out, nil
}

func (s *Storage) DeleteWorkflow(ctx context.Context, id, owner string) error {
	_, err := s.Workflows.DeleteOne(ctx, bson.M{"workflow_id": id, "owner_email": owner})
	if err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	return nil
}

// --- artifacts ---

func (s *Storage) CreateArtifact(ctx context.Context, a *Artifact) error {
	if _, err := s.Artifacts.InsertOne(ctx, a); err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}

func (s *Storage) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	var out Artifact
	err := s.Artifacts.FindOne(ctx, bson.M{"artifact_id": id}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &out, nil
}

func (s *Storage) GetArtifactBySlug(ctx context.Context, slug string) (*Artifact, error) {
	var out Artifact
	err := s.Artifacts.FindOne(ctx, bson.M{"slug": slug}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact by slug: %w", err)
	}
	return &out, nil
}

// GetArtifactByConversation finds the artifact attached to a conversation (one
// per conversation), so a step can update it instead of creating a new one.
func (s *Storage) GetArtifactByConversation(ctx context.Context, convID string) (*Artifact, error) {
	var out Artifact
	err := s.Artifacts.FindOne(ctx, bson.M{"conversation_id": convID}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact by conversation: %w", err)
	}
	return &out, nil
}

func (s *Storage) ListArtifacts(ctx context.Context, owner string) ([]Artifact, error) {
	var out []Artifact
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetLimit(200)
	cur, err := s.Artifacts.Find(ctx, bson.M{"owner_email": owner}, opts)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode artifacts: %w", err)
	}
	return out, nil
}

// AppendArtifactVersion stores a new immutable version and bumps the artifact's
// current_version + updated_at. Returns the new version number.
func (s *Storage) AppendArtifactVersion(ctx context.Context, id string, blocks []ArtifactBlock, note string) (int, error) {
	art, err := s.GetArtifact(ctx, id)
	if err != nil {
		return 0, err
	}
	v := art.CurrentVersion + 1
	ver := ArtifactVersion{ArtifactID: id, Version: v, Blocks: blocks, Note: note, CreatedAt: time.Now().UnixMilli()}
	if _, err := s.ArtifactVers.InsertOne(ctx, &ver); err != nil {
		return 0, fmt.Errorf("insert artifact version: %w", err)
	}
	if _, err := s.Artifacts.UpdateOne(ctx, bson.M{"artifact_id": id},
		bson.M{"$set": bson.M{"current_version": v, "updated_at": ver.CreatedAt}}); err != nil {
		return 0, fmt.Errorf("bump artifact version: %w", err)
	}
	return v, nil
}

// GetArtifactVersion returns a specific version (0/negative → current).
func (s *Storage) GetArtifactVersion(ctx context.Context, id string, version int) (*ArtifactVersion, error) {
	if version <= 0 {
		art, err := s.GetArtifact(ctx, id)
		if err != nil {
			return nil, err
		}
		version = art.CurrentVersion
	}
	var out ArtifactVersion
	err := s.ArtifactVers.FindOne(ctx, bson.M{"artifact_id": id, "version": version}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact version: %w", err)
	}
	return &out, nil
}

// ListArtifactVersions returns version metadata (without blocks) newest first.
func (s *Storage) ListArtifactVersions(ctx context.Context, id string) ([]ArtifactVersion, error) {
	var out []ArtifactVersion
	opts := options.Find().SetSort(bson.D{{Key: "version", Value: -1}}).SetProjection(bson.M{"blocks": 0})
	cur, err := s.ArtifactVers.Find(ctx, bson.M{"artifact_id": id}, opts)
	if err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode artifact versions: %w", err)
	}
	return out, nil
}

func (s *Storage) UpdateArtifactTitle(ctx context.Context, id, title string) error {
	_, err := s.Artifacts.UpdateOne(ctx, bson.M{"artifact_id": id}, bson.M{"$set": bson.M{"title": title}})
	if err != nil {
		return fmt.Errorf("update artifact title: %w", err)
	}
	return nil
}

func (s *Storage) UpdateArtifactShares(ctx context.Context, id string, shares []ConversationShare) error {
	if shares == nil {
		shares = []ConversationShare{}
	}
	_, err := s.Artifacts.UpdateOne(ctx, bson.M{"artifact_id": id}, bson.M{"$set": bson.M{"shares": shares}})
	if err != nil {
		return fmt.Errorf("update artifact shares: %w", err)
	}
	return nil
}

// SetArtifactVisibility flips private/shared/public and (re)sets the share token.
func (s *Storage) SetArtifactVisibility(ctx context.Context, id, visibility, token string) error {
	_, err := s.Artifacts.UpdateOne(ctx, bson.M{"artifact_id": id},
		bson.M{"$set": bson.M{"visibility": visibility, "share_token": token}})
	if err != nil {
		return fmt.Errorf("set artifact visibility: %w", err)
	}
	return nil
}

func (s *Storage) DeleteArtifact(ctx context.Context, id, owner string) error {
	if _, err := s.Artifacts.DeleteOne(ctx, bson.M{"artifact_id": id, "owner_email": owner}); err != nil {
		return fmt.Errorf("delete artifact: %w", err)
	}
	_, _ = s.ArtifactVers.DeleteMany(ctx, bson.M{"artifact_id": id})
	return nil
}

// --- notifications ---

func (s *Storage) CreateNotification(ctx context.Context, n *Notification) error {
	if _, err := s.Notifications.InsertOne(ctx, n); err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

func (s *Storage) ListNotifications(ctx context.Context, owner string, unreadOnly bool) ([]Notification, error) {
	filter := bson.M{"owner_email": owner}
	if unreadOnly {
		filter["read"] = false
	}
	var out []Notification
	if err := paginate(ctx, s.Notifications, filter, 0, 50, &out); err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return out, nil
}

func (s *Storage) UnreadCount(ctx context.Context, owner string) (int64, error) {
	return s.Notifications.CountDocuments(ctx, bson.M{"owner_email": owner, "read": false})
}

// MarkNotificationsRead marks one (id != "") or all of a user's notifications read.
func (s *Storage) MarkNotificationsRead(ctx context.Context, owner, id string) error {
	filter := bson.M{"owner_email": owner}
	if id != "" {
		filter["notification_id"] = id
	}
	_, err := s.Notifications.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"read": true}})
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
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
