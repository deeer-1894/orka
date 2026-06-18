package tools

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // bundle the IANA zoneinfo so timezone works without host tzdata

	qrcode "github.com/skip2/go-qrcode"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/util"
)

var fxClient = &http.Client{Timeout: 12 * time.Second}

// trunc shortens a string to n runes with an ellipsis (office tools share it).
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// currencyConvert converts an amount between currencies using the keyless
// open.er-api.com daily rates — handy for quotes, budgets, expense reports.
func currencyConvert() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from := strings.ToUpper(strings.TrimSpace(req.GetString("from", "")))
		to := strings.ToUpper(strings.TrimSpace(req.GetString("to", "")))
		amount, _ := strconv.ParseFloat(strings.TrimSpace(req.GetString("amount", "1")), 64)
		if from == "" || to == "" {
			return mcp.NewToolResultError("from and to currency codes are required (e.g. USD, CNY)"), nil
		}
		if amount == 0 {
			amount = 1
		}
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://open.er-api.com/v6/latest/"+from, nil)
		resp, err := fxClient.Do(r)
		if err != nil {
			return mcp.NewToolResultError("fx lookup failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		var doc struct {
			Result string             `json:"result"`
			Rates  map[string]float64 `json:"rates"`
			Date   string             `json:"time_last_update_utc"`
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if json.Unmarshal(body, &doc) != nil || doc.Result != "success" {
			return mcp.NewToolResultError("unknown currency or rate service unavailable: " + from), nil
		}
		rate, ok := doc.Rates[to]
		if !ok {
			return mcp.NewToolResultError("no rate for target currency: " + to), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("%.2f %s = %.2f %s  (1 %s = %.4f %s, %s)",
			amount, from, amount*rate, to, from, rate, to, doc.Date)), nil
	}
}

// timezoneConvert converts a wall-clock time from one IANA timezone to another.
func timezoneConvert() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromTZ, err := time.LoadLocation(strings.TrimSpace(req.GetString("from", "")))
		if err != nil {
			return mcp.NewToolResultError("invalid 'from' timezone (use IANA names like Asia/Shanghai): " + err.Error()), nil
		}
		toTZ, err := time.LoadLocation(strings.TrimSpace(req.GetString("to", "")))
		if err != nil {
			return mcp.NewToolResultError("invalid 'to' timezone: " + err.Error()), nil
		}
		raw := strings.TrimSpace(req.GetString("time", ""))
		var t time.Time
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339, "2006-01-02"} {
			if parsed, perr := time.ParseInLocation(layout, raw, fromTZ); perr == nil {
				t = parsed
				break
			}
		}
		if t.IsZero() {
			if raw != "" {
				return mcp.NewToolResultError("could not parse time " + strconv.Quote(raw) + " (use 2006-01-02 15:04)"), nil
			}
			t = time.Now().In(fromTZ)
		}
		out := t.In(toTZ)
		return mcp.NewToolResultText(fmt.Sprintf("%s (%s)  →  %s (%s)",
			t.Format("2006-01-02 15:04"), fromTZ, out.Format("2006-01-02 15:04 Mon"), toTZ)), nil
	}
}

// qrGenerate renders text/a URL into a QR-code PNG saved to the workspace.
func qrGenerate(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := req.GetString("text", "")
		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultError("text is required"), nil
		}
		size := req.GetInt("size", 256)
		if size < 64 || size > 1024 {
			size = 256
		}
		rel := req.GetString("path", "qrcode.png")
		if !strings.HasSuffix(strings.ToLower(rel), ".png") {
			rel += ".png"
		}
		png, err := qrcode.Encode(text, qrcode.Medium, size)
		if err != nil {
			return mcp.NewToolResultError("qr encode failed: " + err.Error()), nil
		}
		p, err := util.ResolvePath(base, identity.From(ctx).Email, rel)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := os.WriteFile(p, png, 0o644); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("QR code (%dx%d) for %q saved to %s", size, size, trunc(text, 60), rel)), nil
	}
}

// ---- CSV / table tools ----

func readCSV(base, email, rel string) ([][]string, error) {
	p, err := util.ResolvePath(base, email, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

func colIndex(header []string, name string) int {
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(name)) {
			return i
		}
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 0 && n < len(header) {
		return n
	}
	return -1
}

// csvQuery filters a workspace CSV by `col=value` and selects columns.
func csvQuery(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rows, err := readCSV(base, identity.From(ctx).Email, req.GetString("path", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(rows) < 1 {
			return mcp.NewToolResultText("(empty csv)"), nil
		}
		header := rows[0]
		var fCol = -1
		var fVal string
		if filter := req.GetString("filter", ""); filter != "" {
			if k, v, ok := strings.Cut(filter, "="); ok {
				fCol, fVal = colIndex(header, k), strings.TrimSpace(v)
			}
		}
		var keep []int // selected columns
		if cols := req.GetString("columns", ""); cols != "" {
			for _, c := range strings.Split(cols, ",") {
				if i := colIndex(header, strings.TrimSpace(c)); i >= 0 {
					keep = append(keep, i)
				}
			}
		}
		limit := req.GetInt("limit", 50)
		if limit <= 0 || limit > 500 {
			limit = 50
		}
		project := func(row []string) []string {
			if len(keep) == 0 {
				return row
			}
			out := make([]string, 0, len(keep))
			for _, i := range keep {
				if i < len(row) {
					out = append(out, row[i])
				}
			}
			return out
		}
		var sb strings.Builder
		sb.WriteString(strings.Join(project(header), " | ") + "\n")
		n := 0
		for _, row := range rows[1:] {
			if fCol >= 0 && (fCol >= len(row) || !strings.EqualFold(strings.TrimSpace(row[fCol]), fVal)) {
				continue
			}
			sb.WriteString(strings.Join(project(row), " | ") + "\n")
			if n++; n >= limit {
				break
			}
		}
		fmt.Fprintf(&sb, "\n(%d row(s) matched)", n)
		return mcp.NewToolResultText(sb.String()), nil
	}
}

// csvStats computes count/sum/avg/min/max on a numeric column, optionally grouped.
func csvStats(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rows, err := readCSV(base, identity.From(ctx).Email, req.GetString("path", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(rows) < 2 {
			return mcp.NewToolResultText("(no data rows)"), nil
		}
		header := rows[0]
		vCol := colIndex(header, req.GetString("column", ""))
		if vCol < 0 {
			return mcp.NewToolResultError("column not found: " + req.GetString("column", "")), nil
		}
		gCol := colIndex(header, req.GetString("group_by", ""))

		type agg struct{ n int; sum, min, max float64 }
		groups := map[string]*agg{}
		order := []string{}
		for _, row := range rows[1:] {
			if vCol >= len(row) {
				continue
			}
			val, perr := strconv.ParseFloat(strings.TrimSpace(row[vCol]), 64)
			if perr != nil {
				continue
			}
			key := "ALL"
			if gCol >= 0 && gCol < len(row) {
				key = row[gCol]
			}
			a := groups[key]
			if a == nil {
				a = &agg{min: val, max: val}
				groups[key] = a
				order = append(order, key)
			}
			a.n++
			a.sum += val
			if val < a.min {
				a.min = val
			}
			if val > a.max {
				a.max = val
			}
		}
		sort.Slice(order, func(i, j int) bool { return groups[order[i]].sum > groups[order[j]].sum })
		var sb strings.Builder
		fmt.Fprintf(&sb, "stats on %q%s:\n", header[vCol], func() string {
			if gCol >= 0 {
				return " grouped by " + header[gCol]
			}
			return ""
		}())
		fmt.Fprintf(&sb, "%-20s %6s %12s %12s %10s %10s\n", "group", "count", "sum", "avg", "min", "max")
		for _, k := range order {
			a := groups[k]
			fmt.Fprintf(&sb, "%-20s %6d %12.2f %12.2f %10.2f %10.2f\n", trunc(k, 20), a.n, a.sum, a.sum/float64(a.n), a.min, a.max)
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

// csvToJSON converts a workspace CSV to a JSON array of objects.
func csvToJSON(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rows, err := readCSV(base, identity.From(ctx).Email, req.GetString("path", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(rows) < 1 {
			return mcp.NewToolResultText("[]"), nil
		}
		header := rows[0]
		out := make([]map[string]string, 0, len(rows)-1)
		limit := req.GetInt("limit", 200)
		for i, row := range rows[1:] {
			if i >= limit {
				break
			}
			obj := map[string]string{}
			for j, h := range header {
				if j < len(row) {
					obj[h] = row[j]
				}
			}
			out = append(out, obj)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return mcp.NewToolResultText(buf.String()), nil
	}
}
