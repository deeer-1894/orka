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

// Notification surfaces something the user should know about an unattended run
// (a scheduled/webhook run that failed) — the alerting half of "敢托付":
// run history records WHAT happened; notifications make sure you find out.
type Notification struct {
	NotificationID string `bson:"notification_id" json:"notification_id"`
	OwnerEmail     string `bson:"owner_email" json:"owner_email"`
	Kind           string `bson:"kind" json:"kind"` // run_failed
	Title          string `bson:"title" json:"title"`
	Body           string `bson:"body" json:"body"`
	RunID          string `bson:"run_id" json:"run_id"`
	ConversationID string `bson:"conversation_id" json:"conversation_id"`
	Read           bool   `bson:"read" json:"read"`
	CreatedAt      int64  `bson:"created_at" json:"created_at"`
}

// Connector is a user-registered external MCP server. Orka's MCP gateway, RBAC
// and per-user pooling already exist for the built-in tools server; a Connector
// lets a user point the same machinery at ANY MCP server (http/streamable/stdio)
// so its tools join the agent's toolset — turning Orka into an integration
// platform. Headers carry the server's own auth (API key etc.).
type Connector struct {
	ConnectorID string            `bson:"connector_id" json:"connector_id"`
	OwnerEmail  string            `bson:"owner_email" json:"owner_email"`
	Name        string            `bson:"name" json:"name"`
	Transport   string            `bson:"transport" json:"transport"` // http | streamable_http | stdio
	URL         string            `bson:"url" json:"url"`             // http/streamable_http
	Command     string            `bson:"command" json:"command"`     // stdio
	Args        []string          `bson:"args" json:"args"`           // stdio
	Headers     map[string]string `bson:"headers" json:"headers"`     // auth headers (secret; redacted in API list)
	Enabled     bool              `bson:"enabled" json:"enabled"`
	CreatedAt   int64             `bson:"created_at" json:"created_at"`
}

// RunRecord is one EXECUTION of the agent (manual or scheduled) — the audit
// unit of the automation platform. Tasks are definitions; RunRecords are the
// individual runs, so a scheduled task spawns many of them. Captures enough to
// answer "did it run, how long, did it succeed, what did it produce, why did it
// fail" without re-reading the whole conversation.
type RunRecord struct {
	RunID          string `bson:"run_id" json:"run_id"`
	TaskID         string `bson:"task_id" json:"task_id"` // parent scheduled task ("" = ad-hoc)
	ConversationID string `bson:"conversation_id" json:"conversation_id"`
	OwnerEmail     string `bson:"owner_email" json:"owner_email"`
	Trigger        string `bson:"trigger" json:"trigger"` // manual | schedule | resume
	Status         string `bson:"status" json:"status"`   // running | done | failed | paused
	Prompt         string `bson:"prompt" json:"prompt"`
	Output         string `bson:"output" json:"output"` // final answer summary (capped)
	Error          string `bson:"error" json:"error"`
	ToolCalls      int    `bson:"tool_calls" json:"tool_calls"`
	Tokens         int    `bson:"tokens" json:"tokens"`
	TraceID        string `bson:"trace_id" json:"trace_id"` // ties to messages/spans of this run
	CreatedAt      int64  `bson:"created_at" json:"created_at"`
	FinishedAt     int64  `bson:"finished_at" json:"finished_at"`
	DurationMs     int64  `bson:"duration_ms" json:"duration_ms"`
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
	IntervalSec       int64          `bson:"interval_sec" json:"interval_sec"` // recurring period (0 = not scheduled)
	NextRunAt         int64          `bson:"next_run_at" json:"next_run_at"`   // unix millis the cron is next due
	LastResult        string         `bson:"last_result" json:"last_result"`   // short summary of the latest run
	WebhookToken      string         `bson:"webhook_token,omitempty" json:"webhook_token,omitempty"` // set → POST /hook/{token} triggers this task
	RetryCount        int            `bson:"retry_count,omitempty" json:"retry_count,omitempty"`     // task-level auto-retry on failure (0 = none)
}
