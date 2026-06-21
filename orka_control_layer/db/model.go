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
	// Shares grants other users access to this conversation (read-only by
	// default). Empty = private to the owner. The owner stays the single source
	// of identity for file/workspace effects, even when an editor sends.
	Shares []ConversationShare `bson:"shares,omitempty" json:"shares,omitempty"`
}

// ConversationShare grants one user access to a conversation.
type ConversationShare struct {
	Email string `bson:"email" json:"email"`
	Role  string `bson:"role" json:"role"` // viewer (read-only) | editor (may also send)
}

// Conversation roles.
const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
)

// CanRead reports whether email may view this conversation.
func (c *ConversationTable) CanRead(email string) bool {
	if email == "" {
		return false
	}
	if c.OwnerEmail == "" || c.OwnerEmail == email {
		return true
	}
	for _, s := range c.Shares {
		if s.Email == email {
			return true
		}
	}
	return false
}

// CanWrite reports whether email may send messages in this conversation (owner
// or an explicit editor). Viewers are read-only.
func (c *ConversationTable) CanWrite(email string) bool {
	if email == "" {
		return false
	}
	if c.OwnerEmail == "" || c.OwnerEmail == email {
		return true
	}
	for _, s := range c.Shares {
		if s.Email == email && s.Role == RoleEditor {
			return true
		}
	}
	return false
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

// Workflow is a user-defined ordered pipeline of steps. Each step runs as a turn
// in one shared conversation, so a step sees prior steps' output via conversation
// memory — deterministic, repeatable multi-step automation built on the run
// engine (schedulable + webhook-triggerable like any task).
type Workflow struct {
	WorkflowID string         `bson:"workflow_id" json:"workflow_id"`
	OwnerEmail string         `bson:"owner_email" json:"owner_email"`
	Name       string         `bson:"name" json:"name"`
	Steps      []WorkflowStep `bson:"steps" json:"steps"`
	CreatedAt  int64          `bson:"created_at" json:"created_at"`
}

// Artifact is a live, shareable visualization page generated from a
// conversation's context. Its content is versioned (each publish = a new
// ArtifactVersion); the page renders the current version and refreshes in place
// when a new one is published. Visibility follows the same model as
// conversations, plus an optional public share link.
type Artifact struct {
	ArtifactID     string              `bson:"artifact_id" json:"artifact_id"`
	OwnerEmail     string              `bson:"owner_email" json:"owner_email"`
	ConversationID string              `bson:"conversation_id" json:"conversation_id"` // source of context
	Title          string              `bson:"title" json:"title"`
	Kind           string              `bson:"kind" json:"kind"` // pr_review|architecture|incident|checklist|audit|custom
	Slug           string              `bson:"slug" json:"slug"` // stable public URL segment
	Visibility     string              `bson:"visibility" json:"visibility"` // private | shared | public
	Shares         []ConversationShare `bson:"shares,omitempty" json:"shares,omitempty"`
	ShareToken     string              `bson:"share_token,omitempty" json:"share_token,omitempty"` // public-link auth
	CurrentVersion int                 `bson:"current_version" json:"current_version"`
	CreatedAt      int64               `bson:"created_at" json:"created_at"`
	UpdatedAt      int64               `bson:"updated_at" json:"updated_at"`
}

// ArtifactVersion is one immutable published snapshot of an artifact's content.
type ArtifactVersion struct {
	ArtifactID string          `bson:"artifact_id" json:"artifact_id"`
	Version    int             `bson:"version" json:"version"`
	Blocks     []ArtifactBlock `bson:"blocks" json:"blocks"`
	Note       string          `bson:"note,omitempty" json:"note,omitempty"` // what changed this publish
	CreatedAt  int64           `bson:"created_at" json:"created_at"`
}

// ArtifactBlock is one typed content block. Data is interpreted by Type:
//
//	markdown {text}        heading {text, level}
//	table {columns[], rows[][]}   checklist {items:[{label,status}]}
//	metric {label, value, delta}  diff {path, patch}
//	timeline {events:[{time,title,detail}]}  code {language, text}
//	badge {label, tone}    mermaid {src}   html {src}  (sandboxed)
type ArtifactBlock struct {
	Type string         `bson:"type" json:"type"`
	Data map[string]any `bson:"data" json:"data"`
}

// Artifact visibility values.
const (
	ArtifactPrivate = "private"
	ArtifactShared  = "shared"
	ArtifactPublic  = "public"
)

// CanRead reports whether email may view this artifact (independent of the
// public link, which is checked separately by token).
func (a *Artifact) CanRead(email string) bool {
	if email != "" && (a.OwnerEmail == "" || a.OwnerEmail == email) {
		return true
	}
	for _, s := range a.Shares {
		if s.Email == email && email != "" {
			return true
		}
	}
	return false
}

// WorkflowStep is one node of a workflow DAG. Beyond a prompt it carries the
// flow-control that lifts workflows past a linear pipeline:
//   - DependsOn: names of steps that must finish first (the DAG edges). Empty =
//     an entry node. Independent steps in the same "layer" may run in parallel.
//   - RunIf: a guard like `research contains FOUND` evaluated against prior step
//     outputs; if false the step is skipped (its dependents still run).
//   - OnError: stop | continue | retry:N — what to do when this step fails.
// Prompts and RunIf may reference a prior step's output with {{step_name}}.
type WorkflowStep struct {
	Name      string   `bson:"name" json:"name"`
	Prompt    string   `bson:"prompt" json:"prompt"`
	DependsOn []string `bson:"depends_on,omitempty" json:"depends_on,omitempty"`
	RunIf     string   `bson:"run_if,omitempty" json:"run_if,omitempty"`
	OnError   string   `bson:"on_error,omitempty" json:"on_error,omitempty"` // stop (default) | continue | retry:N
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
	Result         string `bson:"result,omitempty" json:"result,omitempty"` // structured JSON extracted from the answer (chaining / external consumption)
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
