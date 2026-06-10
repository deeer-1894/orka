// Package db holds the Mongo data models and a thin CRUD wrapper.
package db

// Task run statuses.
const (
	RunStart   = "start"
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
	RunPaused  = "paused"
)

// User is an account record (password stored as a bcrypt hash).
type User struct {
	Email        string `bson:"email" json:"email"`
	Name         string `bson:"name" json:"name"`
	PasswordHash string `bson:"password_hash" json:"-"`
	CreatedAt    int64  `bson:"created_at" json:"created_at"`
}

// ConversationTable is the conversation shell; one conversation may own many tasks.
type ConversationTable struct {
	ConversationID string   `bson:"conversation_id" json:"conversation_id"`
	OwnerEmail     string   `bson:"owner_email" json:"owner_email"`
	Title          string   `bson:"title" json:"title"`
	TaskIds        []string `bson:"task_ids" json:"task_ids"`
	CreatedAt      int64    `bson:"created_at" json:"created_at"`
}

// MessagesTable is the event-message model (not a plain chat transcript).
type MessagesTable struct {
	ID             string `bson:"_id" json:"id"`
	ConversationID string `bson:"conversation_id" json:"conversation_id"`
	Role           string `bson:"role" json:"role"`
	Content        string `bson:"content" json:"content"`
	Meta           any    `bson:"meta" json:"meta"`
	Action         string `bson:"action" json:"action"`
	Payload        any    `bson:"payload" json:"payload"`
	Type           string `bson:"type" json:"type"`
	CreatedAt      int64  `bson:"created_at" json:"created_at"`
}

// TaskMeta supports template-driven, cron-scheduled, variable-rendered tasks.
type TaskMeta struct {
	TaskID            string         `bson:"task_id" json:"task_id"`
	InitialTemplateId string         `bson:"initial_template_id" json:"initial_template_id"`
	CronStatus        string         `bson:"cron_status" json:"cron_status"`
	RunStatus         string         `bson:"run_status" json:"run_status"`
	ConversationID    string         `bson:"conversation_id" json:"conversation_id"`
	OwnerEmail        string         `bson:"owner_email" json:"owner_email"`
	Variables         map[string]any `bson:"variables" json:"variables"`
	CreatedAt         int64          `bson:"created_at" json:"created_at"`
}
