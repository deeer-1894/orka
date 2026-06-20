// Package tools defines the gateway's tools: real file tools confined to the
// per-user storage root, plus Lark/AIO stubs. Each tool carries a ToolGroup and
// a required RBAC scope.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/util"
)

// Meta describes a tool's group and the scope required to use it.
type Meta struct {
	Group string
	Scope string
}

// Registry maps tool name -> Meta. Used by the gateway's tool filter (list) and
// the per-handler guard (call).
func Registry() map[string]Meta {
	return map[string]Meta{
		"file_read":   {Group: "file", Scope: "file:read"},
		"file_write":  {Group: "file", Scope: "file:write"},
		"file_list":   {Group: "file", Scope: "file:read"},
		"web_search":   {Group: "web", Scope: "web:search"},
		"fetch_url":    {Group: "web", Scope: "web:search"},
		"weather":      {Group: "web", Scope: "web:search"},
		"current_time": {Group: "util", Scope: ""}, // always available
		"calculator":   {Group: "util", Scope: ""},
		"unit_convert": {Group: "util", Scope: ""},
		"base64":       {Group: "util", Scope: ""},
		"hash":         {Group: "util", Scope: ""},
		"uuid":         {Group: "util", Scope: ""},
		"json_format":  {Group: "util", Scope: ""},
		"text_stats":   {Group: "util", Scope: ""},
		"regex_extract": {Group: "util", Scope: ""},
		"json_query":    {Group: "util", Scope: ""},
		"datetime":      {Group: "util", Scope: ""},
		"random":        {Group: "util", Scope: ""},
		"memory":        {Group: "file", Scope: "file:write"}, // persists to the user's storage
		"http_request": {Group: "web", Scope: "web:search"}, // network egress → gated
		"shell":        {Group: "shell", Scope: ""},         // env-gated (SHELL_TOOL=1); confined to the workspace
		// Office / productivity tools.
		"currency":    {Group: "office", Scope: ""},
		"timezone":    {Group: "office", Scope: ""},
		"qrcode":      {Group: "office", Scope: "file:write"},
		"csv_query":   {Group: "office", Scope: "file:read"},
		"csv_stats":   {Group: "office", Scope: "file:read"},
		"csv_to_json": {Group: "office", Scope: "file:read"},
		"doc_export":  {Group: "office", Scope: "file:write"},
		"chart":       {Group: "office", Scope: "file:write"},
		"xlsx_to_csv": {Group: "office", Scope: "file:write"},
		"csv_to_xlsx": {Group: "office", Scope: "file:write"},
		"pdf_extract": {Group: "office", Scope: "file:read"},
		"doc_read":    {Group: "office", Scope: "file:read"},
		"sql_query":   {Group: "office", Scope: "file:read"},
		"csv_join":    {Group: "office", Scope: "file:write"},
		"slides":      {Group: "office", Scope: "file:write"},
		"python":      {Group: "code", Scope: ""}, // confined to the workspace container, like shell
		"lark_whoami": {Group: "lark", Scope: "lark:read"},
		"aio_echo":    {Group: "aio", Scope: "aio:read"},
	}
}

// Register adds all non-blacklisted tools to the server, wrapping handlers with
// a scope guard (defense in depth: list filtering is not enough on its own).
func Register(s *mcpserver.MCPServer, baseStorage string, blacklist map[string]bool) {
	reg := Registry()
	add := func(tool mcp.Tool, h mcpserver.ToolHandlerFunc) {
		if blacklist[tool.Name] {
			return
		}
		s.AddTool(tool, guard(reg[tool.Name].Scope, h))
	}

	add(mcp.NewTool("file_read",
		mcp.WithDescription("Read a UTF-8 text file from your storage."),
		mcp.WithString("path", mcp.Required(), mcp.Description("relative file path")),
	), fileRead(baseStorage))

	add(mcp.NewTool("file_write",
		mcp.WithDescription("Write a UTF-8 text file to your storage (creates dirs)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("relative file path")),
		mcp.WithString("content", mcp.Required(), mcp.Description("file content")),
	), fileWrite(baseStorage))

	add(mcp.NewTool("file_list",
		mcp.WithDescription("List directory entries in your storage."),
		mcp.WithString("path", mcp.Description("relative directory path (default root)")),
	), fileList(baseStorage))

	add(mcp.NewTool("web_search",
		mcp.WithDescription("Search the web (DuckDuckGo) and return the top results. Use this for facts, weather, news, docs — not the GUI browser."),
		mcp.WithString("query", mcp.Required(), mcp.Description("search query")),
		mcp.WithNumber("limit", mcp.Description("max results (default 5)")),
	), webSearch())

	add(mcp.NewTool("fetch_url",
		mcp.WithDescription("Fetch a web page and return its readable text. Use after web_search to read a result."),
		mcp.WithString("url", mcp.Required(), mcp.Description("the page URL")),
	), fetchURL())

	add(mcp.NewTool("weather",
		mcp.WithDescription("Get current weather + today's forecast for a location (live, keyless)."),
		mcp.WithString("location", mcp.Required(), mcp.Description("city name, e.g. 西安 / Xian")),
	), weather())

	add(mcp.NewTool("current_time",
		mcp.WithDescription("Get the current date, time and weekday. Use for any 'today/now/recent' question."),
		mcp.WithString("timezone", mcp.Description("IANA tz, default Asia/Shanghai")),
	), currentTime())

	add(mcp.NewTool("calculator",
		mcp.WithDescription("Evaluate an arithmetic expression: + - * / % ^ and parentheses."),
		mcp.WithString("expression", mcp.Required(), mcp.Description("e.g. (3+4)*2^3")),
	), calculator())

	add(mcp.NewTool("unit_convert",
		mcp.WithDescription("Convert a value between units (length, mass, data, time, temperature)."),
		mcp.WithNumber("value", mcp.Required(), mcp.Description("the numeric value")),
		mcp.WithString("from", mcp.Required(), mcp.Description("source unit, e.g. km, lb, GiB, C")),
		mcp.WithString("to", mcp.Required(), mcp.Description("target unit, e.g. mi, kg, MB, F")),
	), unitConvert())

	add(mcp.NewTool("base64",
		mcp.WithDescription("Encode or decode text to/from base64."),
		mcp.WithString("text", mcp.Required(), mcp.Description("the input text")),
		mcp.WithString("mode", mcp.Description("encode (default) or decode")),
	), base64Tool())

	add(mcp.NewTool("hash",
		mcp.WithDescription("Compute a cryptographic hash of text (md5/sha1/sha256)."),
		mcp.WithString("text", mcp.Required(), mcp.Description("the input text")),
		mcp.WithString("algo", mcp.Description("sha256 (default), sha1, or md5")),
	), hashTool())

	add(mcp.NewTool("uuid",
		mcp.WithDescription("Generate a random UUID (v4)."),
	), uuidTool())

	add(mcp.NewTool("json_format",
		mcp.WithDescription("Pretty-print or minify a JSON document."),
		mcp.WithString("json", mcp.Required(), mcp.Description("the JSON text")),
		mcp.WithString("mode", mcp.Description("pretty (default) or minify")),
	), jsonFormatTool())

	add(mcp.NewTool("text_stats",
		mcp.WithDescription("Count characters, words, lines and CJK characters in text."),
		mcp.WithString("text", mcp.Required(), mcp.Description("the input text")),
	), textStatsTool())

	add(mcp.NewTool("regex_extract",
		mcp.WithDescription("Find all matches of a regular expression in text — pull structured bits out of fetched pages, logs, or free text."),
		mcp.WithString("text", mcp.Required(), mcp.Description("the text to search")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("a Go/RE2 regular expression")),
	), regexExtract())

	add(mcp.NewTool("json_query",
		mcp.WithDescription("Extract a value from a JSON document by a dotted path with [index], e.g. data.items[0].name. Pair with http_request to read API responses."),
		mcp.WithString("json", mcp.Required(), mcp.Description("the JSON text")),
		mcp.WithString("path", mcp.Required(), mcp.Description("e.g. data.items[0].name")),
	), jsonQuery())

	add(mcp.NewTool("datetime",
		mcp.WithDescription("Date arithmetic: add a duration to a date, diff two dates, or get a date's weekday. Use instead of computing dates in your head."),
		mcp.WithString("op", mcp.Required(), mcp.Description("add | diff | weekday")),
		mcp.WithString("date", mcp.Description("a date, e.g. 2026-06-14")),
		mcp.WithString("date2", mcp.Description("second date (for diff)")),
		mcp.WithNumber("days", mcp.Description("days to add (for add)")),
		mcp.WithNumber("hours", mcp.Description("hours to add (for add)")),
	), datetimeTool())

	add(mcp.NewTool("random",
		mcp.WithDescription("Get a random int/float in a range, or pick from a list of choices."),
		mcp.WithString("type", mcp.Description("int (default) | float | choice")),
		mcp.WithNumber("min", mcp.Description("range minimum")),
		mcp.WithNumber("max", mcp.Description("range maximum")),
		mcp.WithString("choices", mcp.Description("comma-separated options (for choice)")),
	), randomTool())

	add(mcp.NewTool("memory",
		mcp.WithDescription("A persistent key-value scratchpad that survives across runs. Stash intermediate results (set), recall them later (get), list or delete. Use it to carry state through a long, multi-step task."),
		mcp.WithString("op", mcp.Required(), mcp.Description("set | get | list | delete")),
		mcp.WithString("key", mcp.Description("the key")),
		mcp.WithString("value", mcp.Description("the value (for set)")),
	), memoryTool(baseStorage))

	add(mcp.NewTool("http_request",
		mcp.WithDescription("Make an HTTP GET/POST to a public URL (e.g. a JSON API) and return status + body. Not for browser tasks."),
		mcp.WithString("url", mcp.Required(), mcp.Description("the request URL (http/https, public host only)")),
		mcp.WithString("method", mcp.Description("GET (default) or POST")),
		mcp.WithString("body", mcp.Description("request body for POST")),
		mcp.WithString("content_type", mcp.Description("Content-Type for the body")),
	), httpRequest())

	// The shell/terminal is powerful, so it is opt-in via SHELL_TOOL=1 and is
	// confined to the per-user workspace (see shellExec). Run the gateway inside a
	// container/VM for hard isolation when exposing it to untrusted workloads.
	if os.Getenv("SHELL_TOOL") == "1" {
		add(mcp.NewTool("shell",
			mcp.WithDescription("Run a shell command in your workspace (POSIX sh). Use it like a terminal: run CLI tools, scripts, git, package managers, data processing, or code you wrote (e.g. `python3 app.py`, `grep -rn foo .`, `ls -la`). The working directory is your workspace and output is captured. Prefer this over describing manual steps when one command would do the job."),
			mcp.WithString("command", mcp.Required(), mcp.Description("the shell command to run")),
			mcp.WithNumber("timeout_sec", mcp.Description("max seconds before it's killed (default 30, max 120)")),
		), shellExec(baseStorage))
	}

	// ---- Office / productivity tools ----
	add(mcp.NewTool("currency",
		mcp.WithDescription("Convert an amount between currencies using live daily rates. Args: from, to (ISO codes like USD/CNY/EUR), amount."),
		mcp.WithString("from", mcp.Required(), mcp.Description("source currency code, e.g. USD")),
		mcp.WithString("to", mcp.Required(), mcp.Description("target currency code, e.g. CNY")),
		mcp.WithString("amount", mcp.Description("amount to convert (default 1)")),
	), currencyConvert())

	add(mcp.NewTool("timezone",
		mcp.WithDescription("Convert a wall-clock time from one timezone to another. Args: time (YYYY-MM-DD HH:MM, blank = now), from, to (IANA names like Asia/Shanghai, America/New_York)."),
		mcp.WithString("from", mcp.Required(), mcp.Description("source IANA timezone")),
		mcp.WithString("to", mcp.Required(), mcp.Description("target IANA timezone")),
		mcp.WithString("time", mcp.Description("the time to convert (default: now)")),
	), timezoneConvert())

	add(mcp.NewTool("qrcode",
		mcp.WithDescription("Generate a QR-code PNG (for a URL or text) saved to your workspace. Args: text, path (default qrcode.png), size."),
		mcp.WithString("text", mcp.Required(), mcp.Description("the text/URL to encode")),
		mcp.WithString("path", mcp.Description("output filename (default qrcode.png)")),
		mcp.WithNumber("size", mcp.Description("pixel size 64–1024 (default 256)")),
	), qrGenerate(baseStorage))

	add(mcp.NewTool("csv_query",
		mcp.WithDescription("Filter/select rows from a workspace CSV. Args: path, filter (col=value), columns (comma-separated), limit."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .csv file in your workspace")),
		mcp.WithString("filter", mcp.Description("keep rows where col=value")),
		mcp.WithString("columns", mcp.Description("only output these columns (comma-separated)")),
		mcp.WithNumber("limit", mcp.Description("max rows (default 50)")),
	), csvQuery(baseStorage))

	add(mcp.NewTool("csv_stats",
		mcp.WithDescription("Compute count/sum/avg/min/max on a numeric column of a workspace CSV, optionally grouped. Args: path, column, group_by."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .csv file")),
		mcp.WithString("column", mcp.Required(), mcp.Description("the numeric column to aggregate")),
		mcp.WithString("group_by", mcp.Description("optional column to group by")),
	), csvStats(baseStorage))

	add(mcp.NewTool("csv_to_json",
		mcp.WithDescription("Convert a workspace CSV to a JSON array of objects. Args: path, limit."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .csv file")),
		mcp.WithNumber("limit", mcp.Description("max rows (default 200)")),
	), csvToJSON(baseStorage))

	add(mcp.NewTool("doc_export",
		mcp.WithDescription("Export a workspace Markdown file to HTML, Word (docx) or PDF. Args: path (the .md), to (html|docx|pdf), out (optional)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the source .md file")),
		mcp.WithString("to", mcp.Description("html | docx | pdf (default pdf)")),
		mcp.WithString("out", mcp.Description("output filename (optional)")),
	), docExport(baseStorage))

	add(mcp.NewTool("chart",
		mcp.WithDescription("Render a workspace CSV into a bar/line/pie chart PNG. Repeated x labels are aggregated so large tables stay legible (e.g. 1000 sales rows → one bar per product). Args: data (the .csv), type (bar|line|pie), x (label column), y (value column), agg, title, out."),
		mcp.WithString("data", mcp.Required(), mcp.Description("the .csv file to plot")),
		mcp.WithString("type", mcp.Description("bar | line | pie (default bar)")),
		mcp.WithString("x", mcp.Description("label/category column (default first)")),
		mcp.WithString("y", mcp.Description("value column (default second)")),
		mcp.WithString("agg", mcp.Description("aggregate y per x label: auto|sum|avg|count|none (default auto — sums when labels repeat)")),
		mcp.WithString("title", mcp.Description("chart title")),
		mcp.WithString("out", mcp.Description("output png filename (default chart.png)")),
	), chartGenerate(baseStorage))

	add(mcp.NewTool("xlsx_to_csv",
		mcp.WithDescription("Convert an Excel .xlsx sheet in your workspace to CSV so the csv_* tools can analyze it. Args: path (the .xlsx), sheet (name or 0-based index, default first), out (default <name>.csv)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .xlsx file")),
		mcp.WithString("sheet", mcp.Description("sheet name or 0-based index (default first)")),
		mcp.WithString("out", mcp.Description("output .csv filename")),
	), xlsxToCSV(baseStorage))

	add(mcp.NewTool("csv_to_xlsx",
		mcp.WithDescription("Convert a workspace CSV into a formatted Excel .xlsx file. Args: path (the .csv), out (default <name>.xlsx), sheet (sheet name)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .csv file")),
		mcp.WithString("out", mcp.Description("output .xlsx filename")),
		mcp.WithString("sheet", mcp.Description("sheet name (default Sheet1)")),
	), csvToXLSX(baseStorage))

	add(mcp.NewTool("pdf_extract",
		mcp.WithDescription("Extract the text of a workspace PDF (for reading/summarizing). Args: path (the .pdf), pages (e.g. 1-3, optional), out (write to a .txt file; omit to return the text inline)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the .pdf file")),
		mcp.WithString("pages", mcp.Description("page range like 1-3 or 2 (default all)")),
		mcp.WithString("out", mcp.Description("optional .txt output filename")),
	), pdfExtract(baseStorage))

	add(mcp.NewTool("doc_read",
		mcp.WithDescription("Read an office document (Word .docx, .html, .rtf, .odt, .epub) as Markdown text. The inverse of doc_export. Args: path, out (write to a .md file; omit to return inline)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("the document to read")),
		mcp.WithString("out", mcp.Description("optional .md output filename")),
	), docRead(baseStorage))

	add(mcp.NewTool("sql_query",
		mcp.WithDescription("Run a SQL query over one or more workspace CSVs (in-memory SQLite). Each CSV becomes a table named after its filename stem. Args: query (SQL), tables (comma-separated .csv files), out (optional .csv to save results)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("SQL, e.g. SELECT product_name, SUM(amount) FROM sales GROUP BY 1 ORDER BY 2 DESC")),
		mcp.WithString("tables", mcp.Required(), mcp.Description("comma-separated .csv files to load, e.g. sales.csv,products.csv")),
		mcp.WithString("out", mcp.Description("optional .csv filename to save the result")),
	), sqlQuery(baseStorage))

	add(mcp.NewTool("csv_join",
		mcp.WithDescription("Join two workspace CSVs on a key column (like a SQL JOIN). Args: left, right (the .csv files), on (shared key column) or left_on/right_on, how (inner|left|right|outer, default inner), out."),
		mcp.WithString("left", mcp.Required(), mcp.Description("left .csv file")),
		mcp.WithString("right", mcp.Required(), mcp.Description("right .csv file")),
		mcp.WithString("on", mcp.Description("shared key column (or use left_on/right_on)")),
		mcp.WithString("left_on", mcp.Description("left key column (if names differ)")),
		mcp.WithString("right_on", mcp.Description("right key column (if names differ)")),
		mcp.WithString("how", mcp.Description("inner | left | right | outer (default inner)")),
		mcp.WithString("out", mcp.Description("output .csv filename (default joined.csv)")),
	), csvJoin(baseStorage))

	add(mcp.NewTool("slides",
		mcp.WithDescription("Generate a PowerPoint .pptx from Markdown. `# X` is the title slide; each `## Y` starts a new slide whose `- bullets` become its bullet points. Args: content (the Markdown), title (optional title-slide override), out (default slides.pptx)."),
		mcp.WithString("content", mcp.Required(), mcp.Description("Markdown: # title, ## slide headings, - bullets")),
		mcp.WithString("title", mcp.Description("optional title-slide text")),
		mcp.WithString("out", mcp.Description("output .pptx filename (default slides.pptx)")),
	), slidesGenerate(baseStorage))

	add(mcp.NewTool("python",
		mcp.WithDescription("Run Python in the sandboxed workspace and capture its output — for writing code, computing, and data analysis. pandas/numpy/matplotlib/openpyxl are preinstalled; files read/written are relative to your workspace. Args: code (a snippet) OR path (a .py file to run), and optional argv."),
		mcp.WithString("code", mcp.Description("Python source to execute (use this or path)")),
		mcp.WithString("path", mcp.Description("a workspace .py file to run instead of inline code")),
		mcp.WithString("argv", mcp.Description("optional space-separated args passed to the script")),
	), pythonRun(baseStorage))

	add(mcp.NewTool("lark_whoami",
		mcp.WithDescription("Lark: return the current user (stub)."),
	), stub("lark"))

	add(mcp.NewTool("aio_echo",
		mcp.WithDescription("AIO: echo (stub)."),
		mcp.WithString("text", mcp.Description("text")),
	), stub("aio"))
}

// guard enforces the required scope before running the handler.
func guard(scope string, h mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if scope != "" && !identity.From(ctx).HasScope(scope) {
			return mcp.NewToolResultError("permission denied: missing scope " + scope), nil
		}
		return h(ctx, req)
	}
}

func fileRead(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := util.ResolvePath(base, identity.From(ctx).Email, req.GetString("path", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func fileWrite(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rel := req.GetString("path", "")
		p, err := util.ResolvePath(base, identity.From(ctx).Email, rel)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Back up the prior version before overwriting (recoverable + diffable).
		backed := backupBeforeWrite(base, identity.From(ctx).Email, rel)
		content := req.GetString("content", "")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		msg := fmt.Sprintf("wrote %d bytes to %s", len(content), rel)
		if backed {
			msg += " (previous version saved to history)"
		}
		return mcp.NewToolResultText(msg), nil
	}
}

func fileList(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rel := req.GetString("path", "")
		if rel == "" {
			rel = "."
		}
		p, err := util.ResolvePath(base, identity.From(ctx).Email, rel)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var sb strings.Builder
		for _, e := range entries {
			kind := "f"
			if e.IsDir() {
				kind = "d"
			}
			fmt.Fprintf(&sb, "%s\t%s\n", kind, e.Name())
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func stub(name string) mcpserver.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(fmt.Sprintf("[%s] connector not configured in this OSS build", name)), nil
	}
}
