package browsertool

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

// Engine holds no page or run state. The dialer serializes each operation with
// GUI use; DOM reference state lives only in the leased page's isolated world.
type Engine struct{ dialer connectors.BrowserDialer }

func NewEngine(dialer connectors.BrowserDialer) *Engine { return &Engine{dialer: dialer} }

func (e *Engine) Run(ctx context.Context, identity connectors.GUIIdentity, req Request, files FileActions) (result Result, err error) {
	started := time.Now()
	result.Action = req.Action
	defer func() {
		result.ElapsedMS = time.Since(started).Milliseconds()
		if err != nil {
			result.OK = false
			result.Error = actionError(err)
			err = result.Error
		}
	}()
	if err = validateRequest(identity, req); err != nil {
		return
	}
	if e == nil || e.dialer == nil {
		err = NewActionError("unavailable", "Browser transport is unavailable.")
		return
	}
	duration := 45 * time.Second
	if req.TimeoutMS > 0 {
		duration = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	lease, acquireErr := e.dialer.Acquire(ctx, identity, 30*time.Second, duration)
	if acquireErr != nil {
		err = acquireErr
		return
	}
	defer func() {
		info := lease.Info()
		result.PageID = info.PageID
		result.PageEpoch = info.PageEpoch
		if closeErr := lease.Close(); err == nil && closeErr != nil {
			err = NewActionError("outcome_unknown", "Browser operation cleanup was not acknowledged.")
		}
	}()
	opCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	session := Session{Lease: lease, Identity: identity}
	if req.Action == "preview" {
		reader, ok := files.(PreviewReader)
		if !ok {
			err = NewActionError("unavailable", "Workspace HTML preview is unavailable.")
			return
		}
		result.Preview, err = openPreview(opCtx, session, reader, req.Path)
		if err != nil {
			return
		}
	}
	if req.Action == "open" {
		var response struct {
			ErrorText  string `json:"errorText"`
			IsDownload bool   `json:"isDownload"`
		}
		if err = lease.Execute(opCtx, "Page.navigate", map[string]any{"url": req.URL}, &response); err != nil {
			return
		}
		if response.ErrorText != "" {
			err = NewActionError("navigation_error", "Browser navigation failed.")
			return
		}
		if response.IsDownload {
			err = NewActionError("navigation_error", "Navigation started a download; use the download action.")
			return
		}
	}
	if session.ContextID, err = createWorld(opCtx, lease); err != nil {
		return
	}
	switch req.Action {
	case "snapshot":
		err = observe(opCtx, session, &result, req)
	case "open", "preview":
		err = waitFor(opCtx, session, Request{Condition: req.Condition})
		if err == nil {
			err = observe(opCtx, session, &result, req)
		}
	case "wait":
		err = waitFor(opCtx, session, req)
		if err == nil {
			err = observe(opCtx, session, &result, req)
		}
	case "evaluate":
		result.Value, err = evaluate(opCtx, session, req.Expression)
	case "screenshot", "download":
		if files == nil {
			err = NewActionError("unavailable", "Browser file operations are unavailable.")
			return
		}
		result.Files, err = files.Execute(opCtx, session, req)
	default:
		if req.Action == "fill_form" {
			result.Form, err = fillForm(opCtx, session, req.Fields)
		} else {
			err = act(opCtx, session, req)
		}
		if err != nil && result.Form != nil && result.Form.Completed > 0 {
			// Preserve the partial receipt and original error; do not replay earlier fields.
			_ = observe(opCtx, session, &result, Request{Action: "snapshot"})
		}
		if err == nil {
			// A click/key can navigate. Reacquire the main-document world before the
			// receipt, never replay the mutation if that observation fails.
			session.ContextID, err = createWorld(opCtx, lease)
			if err == nil {
				err = observe(opCtx, session, &result, req)
			}
			if err != nil {
				err = NewActionError("outcome_unknown", "Browser action was dispatched but its final observation is unavailable; inspect the page before retrying.")
			}
		}
	}
	result.OK = err == nil
	return
}

func actionError(err error) *ActionError {
	var action *ActionError
	if errors.As(err, &action) {
		return action
	}
	var transport *connectors.BrowserError
	if errors.As(err, &transport) {
		return NewActionError(transport.Code, "Browser transport could not complete the operation.")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewActionError("timeout", "Browser operation timed out.")
	}
	if errors.Is(err, context.Canceled) {
		return NewActionError("cancelled", "Browser operation was cancelled.")
	}
	return NewActionError("browser_error", "Browser operation failed.")
}

func validateRequest(identity connectors.GUIIdentity, r Request) error {
	invalid := func() error { return NewActionError("invalid_request", "Invalid browser action parameters.") }
	if !validIdentityPart(identity.OwnerID) || !validIdentityPart(identity.ConversationID) || !validIdentityPart(identity.RunID) {
		return NewActionError("invalid_identity", "Browser requires a trusted owner, conversation and run.")
	}
	if r.TimeoutMS < 0 || r.TimeoutMS > 60000 {
		return invalid()
	}
	if (r.Ref == "") != (r.SnapshotID == "") || r.Ref != "" && r.Selector != "" {
		return invalid()
	}
	if len(r.Expression) > MaxExpressionBytes || len(r.Text) > 16<<10 || len(r.Value) > 16<<10 {
		return NewActionError("output_limit", "Browser script or input exceeds its size limit.")
	}
	if len(r.Selector) > 4096 || len(r.Ref) > 256 || len(r.SnapshotID) > 512 || len(r.URL) > 8192 {
		return invalid()
	}
	if r.View != "" && r.View != "auto" && r.View != "full" {
		return invalid()
	}
	if len(r.Frame) > 4 || len(r.Frame) > 0 && (r.Ref != "" || strings.TrimSpace(r.Selector) == "") {
		return invalid()
	}
	for _, selector := range r.Frame {
		if strings.TrimSpace(selector) == "" || len(selector) > 1024 {
			return invalid()
		}
	}
	target := r.Ref != "" || strings.TrimSpace(r.Selector) != ""
	switch r.Action {
	case "fill_form":
		if len(r.Fields) == 0 || len(r.Fields) > maxFormFields {
			return invalid()
		}
		total := 0
		for _, field := range r.Fields {
			if (field.Text == nil) == (field.Value == nil) {
				return invalid()
			}
			f := field.request()
			total += len(f.Text) + len(f.Value)
			if err := validateRequest(identity, f); err != nil {
				return err
			}
		}
		if total > 16<<10 {
			return invalid()
		}
	case "preview":
		if !validPreviewPath(r.Path) || len(r.Path) > 1024 {
			return invalid()
		}
	case "open":
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return invalid()
		}
		if r.Condition != "" && r.Condition != "load" && r.Condition != "domcontentloaded" {
			return invalid()
		}
	case "snapshot", "screenshot", "download":
	case "click", "fill", "select", "press":
		if !target {
			return invalid()
		}
		if r.Action == "press" {
			if _, err := parseKey(r.Key); err != nil {
				return err
			}
		}
	case "scroll":
		if math.IsNaN(r.Amount) || math.IsInf(r.Amount, 0) || r.Amount < 0 || r.Amount > 10000 {
			return invalid()
		}
		if r.Direction != "" && r.Direction != "up" && r.Direction != "down" && r.Direction != "left" && r.Direction != "right" {
			return invalid()
		}
	case "wait":
		switch r.Condition {
		case "":
		case "domcontentloaded", "load":
			if target {
				return invalid()
			}
		case "visible", "hidden", "enabled":
			if !target {
				return invalid()
			}
		case "text":
			if r.Text == "" {
				return invalid()
			}
		case "url":
			if r.URL == "" || target {
				return invalid()
			}
		default:
			return invalid()
		}
	case "evaluate":
		if strings.TrimSpace(r.Expression) == "" {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

func validIdentityPart(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256
}
