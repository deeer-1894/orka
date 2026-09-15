package db

import "context"

// UsageLedger is an atomic per-owner ledger. Load returns a detached snapshot;
// CompareAndSwap commits ALL reservations and settlements only if Version still
// matches, then increments it. Implementations must preserve records on errors.
// Version zero means an account has never been persisted.
type UsageLedger interface {
	Load(context.Context, string) (UsageAccount, error)
	CompareAndSwap(context.Context, string, int64, UsageAccount) (bool, error)
}

const (
	UsageReserved  = "reserved"
	UsageKnown     = "known"
	UsageEstimated = "estimated"
	UsageUnknown   = "unknown"
	UsageCancelled = "cancelled"
)

type UsageAccount struct {
	Owner   string       `bson:"_id"`
	Version int64        `bson:"version"`
	Entries []UsageEntry `bson:"entries"`
}

// Tokens is the charged amount: a reservation for in-flight/unknown calls,
// otherwise the measured or explicitly estimated total. Unknown is never zero.
// RunID and CallID jointly identify an attempt; Source attributes all providers
// and auxiliary work without storing prompts, responses or credentials.
type UsageEntry struct {
	RelatedConversationID string `bson:"related_conversation_id,omitempty" json:"related_conversation_id,omitempty"`
	RelatedRunID          string `bson:"related_run_id,omitempty" json:"related_run_id,omitempty"`
	RunID                 string `bson:"run_id" json:"run_id"`
	CallID                string `bson:"call_id" json:"call_id"`
	Source                string `bson:"source" json:"source"`
	Status                string `bson:"status" json:"status"`
	Tokens                int    `bson:"tokens" json:"tokens"`
	ReservedTokens        int    `bson:"reserved_tokens" json:"reserved_tokens"`
	PromptTokens          int    `bson:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens      int    `bson:"completion_tokens" json:"completion_tokens"`
	ReservedAt            int64  `bson:"reserved_at" json:"reserved_at"`
	SettledAt             int64  `bson:"settled_at" json:"settled_at"`
}
