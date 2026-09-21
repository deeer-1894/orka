// Package browsertool provides model-free browser operations on a scoped lease.
package browsertool

import (
	"context"
	"github.com/orka-oss/orka_control_layer/connectors"
)

const (
	MaxSnapshotBytes   = 64 << 10
	MaxSnapshotRefs    = 200
	MaxSnapshotText    = 12000
	MaxExpressionBytes = 16 << 10
	MaxEvaluationBytes = 64 << 10
	MaxFileBytes       = 16 << 20
)

type Request struct {
	Frame      []string    `json:"frame,omitempty"`
	Fields     []FormField `json:"fields,omitempty"`
	View       string      `json:"view,omitempty"`
	Action     string      `json:"action"`
	URL        string      `json:"url,omitempty"`
	Ref        string      `json:"ref,omitempty"`
	SnapshotID string      `json:"snapshot_id,omitempty"`
	Selector   string      `json:"selector,omitempty"`
	Text       string      `json:"text,omitempty"`
	Value      string      `json:"value,omitempty"`
	Key        string      `json:"key,omitempty"`
	Direction  string      `json:"direction,omitempty"`
	Amount     float64     `json:"amount,omitempty"`
	Condition  string      `json:"condition,omitempty"`
	Expression string      `json:"expression,omitempty"`
	Path       string      `json:"path,omitempty"`
	Mode       string      `json:"mode,omitempty"`
	TimeoutMS  int         `json:"timeout_ms,omitempty"`
}

type Result struct {
	Change    *PageChange   `json:"change,omitempty"`
	Progress  *PageProgress `json:"progress,omitempty"`
	Form      *FormResult   `json:"form,omitempty"`
	OK        bool          `json:"ok"`
	Action    string        `json:"action"`
	PageID    string        `json:"page_id,omitempty"`
	PageEpoch int64         `json:"page_epoch,omitempty"`
	URL       string        `json:"url,omitempty"`
	Title     string        `json:"title,omitempty"`
	Snapshot  *Snapshot     `json:"snapshot,omitempty"`
	Files     []FileResult  `json:"files,omitempty"`
	Preview   *FileResult   `json:"preview,omitempty"`
	Value     any           `json:"value,omitempty"`
	ElapsedMS int64         `json:"elapsed_ms"`
	Error     *ActionError  `json:"error,omitempty"`
	Recovery  *Recovery     `json:"recovery,omitempty"`
}

// Recovery records a read-only observation repair after an acknowledged
// action. It is evidence that the current page was inspected, never that the
// mutating action was replayed or that its business outcome was accepted.
type Recovery struct {
	From               string `json:"from"`
	ActionAcknowledged bool   `json:"action_acknowledged"`
}

type Snapshot struct {
	Mode         string    `json:"mode,omitempty"`
	ReadyState   string    `json:"ready_state,omitempty"`
	ID           string    `json:"id"`
	Text         string    `json:"text"`
	Elements     []Element `json:"elements"`
	Frames       []Frame   `json:"frames,omitempty"`
	PixelContent bool      `json:"pixel_content,omitempty"`
	Omitted      bool      `json:"omitted,omitempty"`
}

type SelectOption struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

type Element struct {
	Actions  []string       `json:"actions,omitempty"`
	Frame    []string       `json:"frame,omitempty"`
	Options  []SelectOption `json:"options,omitempty"`
	Ref      string         `json:"ref"`
	Tag      string         `json:"tag"`
	Role     string         `json:"role,omitempty"`
	Name     string         `json:"name,omitempty"`
	Type     string         `json:"type,omitempty"`
	Disabled bool           `json:"disabled,omitempty"`
	Checked  bool           `json:"checked,omitempty"`
	Selected bool           `json:"selected,omitempty"`
	Editable bool           `json:"editable,omitempty"`
}

type Frame struct {
	Path      []string `json:"path,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Handoff   string   `json:"handoff,omitempty"`
	Title     string   `json:"title,omitempty"`
	URL       string   `json:"url,omitempty"`
	Supported bool     `json:"supported"`
}

type FileResult struct {
	Path   string `json:"path"`
	MIME   string `json:"mime"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Session is scoped to one Engine.Run. File handlers must not close/reacquire it.
type Session struct {
	Lease     connectors.BrowserLease
	Identity  connectors.GUIIdentity
	ContextID int64
}

type FileActions interface {
	Execute(context.Context, Session, Request) ([]FileResult, error)
}

type ActionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ActionError) Error() string { return e.Code + ": " + e.Message }
func NewActionError(code, message string) *ActionError {
	return &ActionError{Code: code, Message: message}
}
