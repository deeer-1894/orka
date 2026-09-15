package browsertool

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

func toolSchema() map[string]any {
	str := func(description string, limit int) map[string]any {
		return map[string]any{"type": "string", "description": description, "maxLength": limit}
	}
	props := map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"open", "snapshot", "click", "fill", "select", "press", "scroll", "wait", "evaluate", "screenshot", "download"}},
		"url":         str("HTTP(S) URL for open/download or wait:url; download also accepts current-page blob URLs.", 8192),
		"ref":         str("Element ref from the same run/page snapshot; requires snapshot_id. Do not combine with selector.", 256),
		"snapshot_id": str("Snapshot identifier paired with ref.", 512),
		"selector":    str("CSS selector that identifies exactly one main-document/open-shadow element.", 4096),
		"text":        str("Text for fill (empty clears input) or wait:text.", 16384),
		"value":       str("Option value for select; empty is allowed.", 16384),
		"key":         str("Key for press: Enter, Tab, Escape, arrows, Home/End, PageUp/Down, Space, or a character; optional Ctrl/Alt/Shift/Meta modifiers.", 64),
		"direction":   map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}, "description": "Scroll direction; default down."},
		"amount":      map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 10000, "description": "Scroll distance in pixels; default 600."},
		"condition":   map[string]any{"type": "string", "enum": []string{"domcontentloaded", "load", "visible", "hidden", "enabled", "text", "url"}, "description": "open: domcontentloaded/load. wait: readiness without target; visible/hidden/enabled with target; text needs text and optional target; url needs url without target."},
		"expression":  str("Page JavaScript script or expression for evaluate only; browser context, never host shell. Result is bounded.", MaxExpressionBytes),
		"path":        str("Relative conversation-workspace output path for screenshot/download.", 1024),
		"mode":        map[string]any{"type": "string", "enum": []string{"create", "replace"}, "description": "File write mode; default create, replace preserves history."},
		"timeout_ms":  map[string]any{"type": "integer", "minimum": 1, "maximum": 60000, "description": "Bound this operation; default 45000 ms, maximum 60000 ms."},
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"action"}, "additionalProperties": false}
}

func parseRequest(args map[string]any) (Request, error) {
	var req Request
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > 64<<10 {
		return req, errors.New("arguments must contain bounded JSON values")
	}
	if err = json.Unmarshal(raw, &req); err != nil {
		return req, errors.New("argument types are invalid")
	}
	allowed := map[string]bool{"action": true, "timeout_ms": true}
	add := func(names ...string) {
		for _, name := range names {
			allowed[name] = true
		}
	}
	target := false
	switch req.Action {
	case "open":
		add("url", "condition")
	case "snapshot":
	case "click":
		target = true
	case "fill":
		target = true
		add("text")
	case "select":
		target = true
		add("value")
	case "press":
		target = true
		add("key")
	case "scroll":
		target = true
		add("direction", "amount")
	case "wait":
		target = true
		add("condition", "text", "url")
	case "evaluate":
		add("expression")
	case "screenshot":
		add("path", "mode")
	case "download":
		add("url", "path", "mode")
	default:
		return req, errors.New("unknown browser action")
	}
	if target {
		add("ref", "snapshot_id", "selector")
	}
	limits := map[string]int{"action": 32, "url": 8192, "ref": 256, "snapshot_id": 512, "selector": 4096, "text": 16384, "value": 16384, "key": 64, "direction": 16, "condition": 32, "expression": MaxExpressionBytes, "path": 1024, "mode": 16}
	for name, value := range args {
		if !allowed[name] {
			return req, fmt.Errorf("argument %s is not supported for this action", safeArgumentName(name))
		}
		if value == nil {
			return req, errors.New("arguments cannot be null")
		}
		if limit, ok := limits[name]; ok {
			text, ok := value.(string)
			if !ok || len(text) > limit {
				return req, fmt.Errorf("argument %s has invalid type or length", name)
			}
		}
	}
	has := func(key string) bool { _, ok := args[key]; return ok }
	require := func(key, value string) bool { return has(key) && strings.TrimSpace(value) != "" }
	if has("timeout_ms") && (req.TimeoutMS < 1 || req.TimeoutMS > 60000) {
		return req, errors.New("timeout_ms must be between 1 and 60000")
	}
	if has("ref") != has("snapshot_id") || (has("ref") && (!require("ref", req.Ref) || !require("snapshot_id", req.SnapshotID))) {
		return req, errors.New("ref requires its nonempty snapshot_id")
	}
	if has("selector") && (!require("selector", req.Selector) || has("ref")) {
		return req, errors.New("use one nonempty selector or a ref/snapshot_id pair")
	}
	hasTarget := has("ref") || has("selector")
	switch req.Action {
	case "open", "download":
		if !require("url", req.URL) || !validToolURL(req.URL, req.Action == "download") {
			return req, errors.New("URL must be HTTP(S), or a current-page blob URL for download")
		}
		if req.Action == "open" && req.Condition != "" && req.Condition != "domcontentloaded" && req.Condition != "load" {
			return req, errors.New("open condition must be domcontentloaded or load")
		}
	case "click", "fill", "select", "press":
		if !hasTarget {
			return req, errors.New("action requires an element target")
		}
		if req.Action == "fill" && !has("text") {
			return req, errors.New("fill requires text; an empty string clears the input")
		}
		if req.Action == "select" && !has("value") {
			return req, errors.New("select requires value")
		}
		if req.Action == "press" && !require("key", req.Key) {
			return req, errors.New("press requires a key")
		}
	case "scroll":
		if has("amount") && (req.Amount <= 0 || req.Amount > 10000) {
			return req, errors.New("scroll amount must be positive and at most 10000")
		}
		if req.Direction != "" && req.Direction != "up" && req.Direction != "down" && req.Direction != "left" && req.Direction != "right" {
			return req, errors.New("invalid scroll direction")
		}
	case "evaluate":
		if !require("expression", req.Expression) {
			return req, errors.New("evaluate requires an expression")
		}
	case "wait":
		condition := req.Condition
		if condition == "" {
			if hasTarget {
				condition = "visible"
			} else {
				condition = "domcontentloaded"
			}
		}
		switch condition {
		case "domcontentloaded", "load":
			if hasTarget || has("text") || has("url") {
				return req, errors.New("readiness wait accepts no target/text/url")
			}
		case "visible", "hidden", "enabled":
			if !hasTarget || has("text") || has("url") {
				return req, errors.New("element wait requires a target without text/url")
			}
		case "text":
			if !require("text", req.Text) || has("url") {
				return req, errors.New("text wait requires text without url")
			}
		case "url":
			if hasTarget || has("text") || !require("url", req.URL) || !validToolURL(req.URL, false) {
				return req, errors.New("URL wait requires HTTP(S) url without target/text")
			}
		default:
			return req, errors.New("invalid wait condition")
		}
	}
	if req.Action == "screenshot" || req.Action == "download" {
		if !require("path", req.Path) || req.Path != path.Clean(req.Path) || path.IsAbs(req.Path) || req.Path == "." || req.Path == ".." || strings.ContainsAny(req.Path, "\\:\x00") || strings.HasPrefix(req.Path, "../") {
			return req, errors.New("file path must be a confined relative workspace path")
		}
		if req.Mode != "" && req.Mode != "create" && req.Mode != "replace" {
			return req, errors.New("file mode must be create or replace")
		}
	}
	return req, nil
}
func validToolURL(value string, blob bool) bool {
	if blob && strings.HasPrefix(value, "blob:") {
		return validToolURL(strings.TrimPrefix(value, "blob:"), false)
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}
func safeArgumentName(name string) string {
	if len(name) > 32 {
		return "(unknown)"
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') {
			return "(unknown)"
		}
	}
	return name
}
