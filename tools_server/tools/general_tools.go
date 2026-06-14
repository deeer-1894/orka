package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/util"
)

// regexExtract finds regex matches in text — invaluable for pulling structured
// bits out of fetched web pages / logs / free text.
func regexExtract() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		re, err := regexp.Compile(req.GetString("pattern", ""))
		if err != nil {
			return mcp.NewToolResultError("invalid regex: " + err.Error()), nil
		}
		text := req.GetString("text", "")
		ms := re.FindAllString(text, 200)
		if len(ms) == 0 {
			return mcp.NewToolResultText("(no matches)"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("%d match(es):\n%s", len(ms), strings.Join(ms, "\n"))), nil
	}
}

// jsonQuery extracts a value from JSON by a dotted path with [index] support,
// e.g. "data.items[0].name" — pairs with http_request to read API responses.
func jsonQuery() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var doc any
		if err := json.Unmarshal([]byte(req.GetString("json", "")), &doc); err != nil {
			return mcp.NewToolResultError("invalid json: " + err.Error()), nil
		}
		cur := doc
		for _, seg := range splitPath(req.GetString("path", "")) {
			if idx, ok := asIndex(seg); ok {
				arr, isArr := cur.([]any)
				if !isArr || idx < 0 || idx >= len(arr) {
					return mcp.NewToolResultError(fmt.Sprintf("path: index %d out of range at %q", idx, seg)), nil
				}
				cur = arr[idx]
				continue
			}
			obj, isObj := cur.(map[string]any)
			if !isObj {
				return mcp.NewToolResultError("path: not an object at " + seg), nil
			}
			v, ok := obj[seg]
			if !ok {
				return mcp.NewToolResultError("path: key not found: " + seg), nil
			}
			cur = v
		}
		switch v := cur.(type) {
		case string:
			return mcp.NewToolResultText(v), nil
		default:
			b, _ := json.Marshal(v)
			return mcp.NewToolResultText(string(b)), nil
		}
	}
}

func splitPath(p string) []string {
	p = strings.ReplaceAll(p, "[", ".[")
	var out []string
	for _, s := range strings.Split(p, ".") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func asIndex(seg string) (int, bool) {
	if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
		if n, err := strconv.Atoi(seg[1 : len(seg)-1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// datetimeTool does date arithmetic the model can't reliably do in its head:
// add a duration, diff two dates, or report a date's weekday.
func datetimeTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		op := strings.ToLower(req.GetString("op", "diff"))
		switch op {
		case "add":
			t, err := parseFlexDate(req.GetString("date", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			days := int(req.GetFloat("days", 0))
			hours := int(req.GetFloat("hours", 0))
			out := t.AddDate(0, 0, days).Add(time.Duration(hours) * time.Hour)
			return mcp.NewToolResultText(out.Format("2006-01-02 15:04:05 Mon")), nil
		case "diff":
			a, err1 := parseFlexDate(req.GetString("date", ""))
			b, err2 := parseFlexDate(req.GetString("date2", ""))
			if err1 != nil || err2 != nil {
				return mcp.NewToolResultError("need two valid dates (date, date2)"), nil
			}
			d := b.Sub(a)
			return mcp.NewToolResultText(fmt.Sprintf("%.0f 天 (%.1f 小时)", d.Hours()/24, d.Hours())), nil
		case "weekday":
			t, err := parseFlexDate(req.GetString("date", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(t.Format("2006-01-02") + " 是 " + t.Format("Monday")), nil
		default:
			return mcp.NewToolResultError("op must be add | diff | weekday"), nil
		}
	}
}

func parseFlexDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/01/02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q (try 2006-01-02)", s)
}

// memoryTool is a persistent per-user key-value scratchpad — lets an agent stash
// intermediate results and recall them across steps/runs (huge for long tasks).
// Backed by a JSON file in the user's storage root.
func memoryTool(base string) mcpserver.ToolHandlerFunc {
	const file = ".orka_memory.json"
	load := func(email string) (map[string]string, string) {
		p, err := util.ResolvePath(base, email, file)
		if err != nil {
			return map[string]string{}, ""
		}
		m := map[string]string{}
		if b, rerr := os.ReadFile(p); rerr == nil {
			_ = json.Unmarshal(b, &m)
		}
		return m, p
	}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		email := identity.From(ctx).Email
		m, p := load(email)
		switch strings.ToLower(req.GetString("op", "get")) {
		case "set":
			key := req.GetString("key", "")
			if key == "" || p == "" {
				return mcp.NewToolResultError("set needs a key"), nil
			}
			m[key] = req.GetString("value", "")
			b, _ := json.MarshalIndent(m, "", "  ")
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("saved " + key), nil
		case "delete":
			delete(m, req.GetString("key", ""))
			b, _ := json.MarshalIndent(m, "", "  ")
			_ = os.WriteFile(p, b, 0o644)
			return mcp.NewToolResultText("deleted"), nil
		case "list":
			if len(m) == 0 {
				return mcp.NewToolResultText("(memory empty)"), nil
			}
			var sb strings.Builder
			for k, v := range m {
				fmt.Fprintf(&sb, "%s = %s\n", k, trunc80(v))
			}
			return mcp.NewToolResultText(sb.String()), nil
		default: // get
			v, ok := m[req.GetString("key", "")]
			if !ok {
				return mcp.NewToolResultText("(not set)"), nil
			}
			return mcp.NewToolResultText(v), nil
		}
	}
}

func trunc80(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// randomTool returns a random int/float in a range, or picks from choices.
func randomTool() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		switch strings.ToLower(req.GetString("type", "int")) {
		case "choice":
			parts := strings.Split(req.GetString("choices", ""), ",")
			var cs []string
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" {
					cs = append(cs, p)
				}
			}
			if len(cs) == 0 {
				return mcp.NewToolResultError("choice needs a comma-separated choices list"), nil
			}
			return mcp.NewToolResultText(cs[rand.Intn(len(cs))]), nil
		case "float":
			lo, hi := req.GetFloat("min", 0), req.GetFloat("max", 1)
			return mcp.NewToolResultText(strconv.FormatFloat(lo+rand.Float64()*(hi-lo), 'f', 4, 64)), nil
		default:
			lo, hi := int(req.GetFloat("min", 1)), int(req.GetFloat("max", 100))
			if hi <= lo {
				hi = lo + 1
			}
			return mcp.NewToolResultText(strconv.Itoa(lo + rand.Intn(hi-lo+1))), nil
		}
	}
}
