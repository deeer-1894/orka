package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/tools_server/identity"
)

// These tools extend the office set with Excel I/O, document reading, SQL/joins
// over CSVs, PowerPoint generation, and a general Python runner. The heavy ones
// shell out to python (pandas/openpyxl/python-pptx) or poppler/pandoc, all
// installed in the gateway container and confined to the per-user workspace.

// runPython runs an inline python script (env-configured, argv-free) in the
// workspace and returns its combined output.
func runPython(ctx context.Context, root, script string, env []string) (string, error) {
	return runInWorkspace(ctx, root, "python3", []string{"-c", script}, env)
}

// baseName trims a path to its filename, confining it to the workspace root.
// Unlike filepath.Base it returns "" for empty/"."/"/", so an omitted optional
// `out` argument stays falsy instead of becoming the "." directory.
func baseName(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	if b := filepath.Base(s); b != "." && b != "/" {
		return b
	}
	return ""
}

// xlsxToCSV converts a .xlsx sheet to CSV via pandas.
func xlsxToCSV(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		in := baseName(req.GetString("path", ""))
		if in == "" || in == "." {
			return mcp.NewToolResultError("path (a workspace .xlsx file) is required"), nil
		}
		out := baseName(req.GetString("out", ""))
		if out == "" {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + ".csv"
		}
		env := []string{"XL_IN=" + in, "XL_SHEET=" + req.GetString("sheet", ""), "XL_OUT=" + out}
		msg, err := runPython(ctx, root, xlsxToCSVScript, env)
		if err != nil {
			return mcp.NewToolResultText("xlsx_to_csv failed (needs pandas/openpyxl in the gateway): " + err.Error() + "\n" + trunc(msg, 600)), nil
		}
		return mcp.NewToolResultText(strings.TrimSpace(msg)), nil
	}
}

const xlsxToCSVScript = `
import os, pandas as pd
sheet = os.environ.get("XL_SHEET","").strip()
try: sheet = int(sheet) if sheet != "" else 0
except ValueError: pass
df = pd.read_excel(os.environ["XL_IN"], sheet_name=sheet)
df.to_csv(os.environ["XL_OUT"], index=False)
print(f'{os.environ["XL_IN"]} -> {os.environ["XL_OUT"]} ({len(df)} rows x {len(df.columns)} cols)')
`

// csvToXLSX converts a CSV to a .xlsx workbook via pandas.
func csvToXLSX(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		in := baseName(req.GetString("path", ""))
		if in == "" || in == "." {
			return mcp.NewToolResultError("path (a workspace .csv file) is required"), nil
		}
		out := baseName(req.GetString("out", ""))
		if out == "" {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + ".xlsx"
		}
		env := []string{"CX_IN=" + in, "CX_OUT=" + out, "CX_SHEET=" + req.GetString("sheet", "Sheet1")}
		msg, err := runPython(ctx, root, csvToXLSXScript, env)
		if err != nil {
			return mcp.NewToolResultText("csv_to_xlsx failed (needs pandas/openpyxl in the gateway): " + err.Error() + "\n" + trunc(msg, 600)), nil
		}
		return mcp.NewToolResultText(strings.TrimSpace(msg)), nil
	}
}

const csvToXLSXScript = `
import os, pandas as pd
df = pd.read_csv(os.environ["CX_IN"])
sheet = os.environ.get("CX_SHEET") or "Sheet1"
df.to_excel(os.environ["CX_OUT"], index=False, sheet_name=sheet[:31])
print(f'{os.environ["CX_IN"]} -> {os.environ["CX_OUT"]} ({len(df)} rows x {len(df.columns)} cols)')
`

// pdfExtract pulls the text out of a workspace PDF via poppler's pdftotext.
func pdfExtract(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		in := baseName(req.GetString("path", ""))
		if in == "" || in == "." {
			return mcp.NewToolResultError("path (a workspace .pdf file) is required"), nil
		}
		args := []string{"-layout"}
		if pr := strings.TrimSpace(req.GetString("pages", "")); pr != "" {
			f, l := pr, pr
			if a, b, ok := strings.Cut(pr, "-"); ok {
				f, l = strings.TrimSpace(a), strings.TrimSpace(b)
			}
			args = append(args, "-f", f, "-l", l)
		}
		out := baseName(req.GetString("out", ""))
		if out != "" {
			args = append(args, in, out)
			if msg, err := runInWorkspace(ctx, root, "pdftotext", args, nil); err != nil {
				return mcp.NewToolResultText("pdf_extract failed (needs poppler in the gateway): " + err.Error() + "\n" + trunc(msg, 400)), nil
			}
			if fi, err := os.Stat(filepath.Join(root, out)); err == nil {
				return mcp.NewToolResultText(fmt.Sprintf("extracted %s → %s (%s)", in, out, humanBytes(fi.Size()))), nil
			}
			return mcp.NewToolResultText("extracted " + in + " → " + out), nil
		}
		args = append(args, in, "-") // write to stdout
		msg, err := runInWorkspace(ctx, root, "pdftotext", args, nil)
		if err != nil {
			return mcp.NewToolResultText("pdf_extract failed (needs poppler in the gateway): " + err.Error() + "\n" + trunc(msg, 400)), nil
		}
		if strings.TrimSpace(msg) == "" {
			return mcp.NewToolResultText("(no extractable text — the PDF may be scanned images)"), nil
		}
		return mcp.NewToolResultText(trunc(msg, 12000)), nil
	}
}

// docRead converts an office document to Markdown via pandoc (inverse of doc_export).
func docRead(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		in := baseName(req.GetString("path", ""))
		if in == "" || in == "." {
			return mcp.NewToolResultError("path (a .docx/.html/.rtf/.odt/.epub file) is required"), nil
		}
		out := baseName(req.GetString("out", ""))
		if out != "" {
			if msg, err := runInWorkspace(ctx, root, "pandoc", []string{in, "-t", "gfm", "-o", out}, nil); err != nil {
				return mcp.NewToolResultText("doc_read failed (is pandoc available?): " + err.Error() + "\n" + trunc(msg, 400)), nil
			}
			return mcp.NewToolResultText("read " + in + " → " + out + " (Markdown)"), nil
		}
		msg, err := runInWorkspace(ctx, root, "pandoc", []string{in, "-t", "gfm"}, nil)
		if err != nil {
			return mcp.NewToolResultText("doc_read failed (is pandoc available?): " + err.Error() + "\n" + trunc(msg, 400)), nil
		}
		return mcp.NewToolResultText(trunc(msg, 12000)), nil
	}
}

// sqlQuery loads the named CSVs into in-memory SQLite and runs the query.
func sqlQuery(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		query := strings.TrimSpace(req.GetString("query", ""))
		tables := strings.TrimSpace(req.GetString("tables", ""))
		if query == "" || tables == "" {
			return mcp.NewToolResultError("query and tables (comma-separated .csv files) are required"), nil
		}
		// keep only base filenames so a table arg can't escape the workspace
		var clean []string
		for _, t := range strings.Split(tables, ",") {
			if t = strings.TrimSpace(t); t != "" {
				clean = append(clean, filepath.Base(t))
			}
		}
		env := []string{
			"SQL_QUERY=" + query,
			"SQL_TABLES=" + strings.Join(clean, ","),
			"SQL_OUT=" + baseName(req.GetString("out", "")),
		}
		msg, err := runPython(ctx, root, sqlQueryScript, env)
		if err != nil {
			return mcp.NewToolResultText("sql_query failed: " + err.Error() + "\n" + trunc(msg, 800)), nil
		}
		return mcp.NewToolResultText(trunc(strings.TrimSpace(msg), 8000)), nil
	}
}

const sqlQueryScript = `
import os, sqlite3, pandas as pd
con = sqlite3.connect(":memory:")
for t in [x for x in os.environ["SQL_TABLES"].split(",") if x]:
    name = os.path.splitext(os.path.basename(t))[0]
    pd.read_csv(t).to_sql(name, con, index=False, if_exists="replace")
res = pd.read_sql_query(os.environ["SQL_QUERY"], con)
out = os.environ.get("SQL_OUT","").strip()
if out:
    res.to_csv(out, index=False)
    print(f'{len(res)} row(s) -> {out}')
    print(res.head(20).to_string(index=False))
else:
    print(res.head(100).to_string(index=False))
    if len(res) > 100: print(f'... ({len(res)} rows total)')
`

// csvJoin merges two CSVs on a key column via pandas.
func csvJoin(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		left := baseName(req.GetString("left", ""))
		right := baseName(req.GetString("right", ""))
		if left == "" || left == "." || right == "" || right == "." {
			return mcp.NewToolResultError("left and right (two workspace .csv files) are required"), nil
		}
		on := strings.TrimSpace(req.GetString("on", ""))
		leftOn := strings.TrimSpace(req.GetString("left_on", ""))
		rightOn := strings.TrimSpace(req.GetString("right_on", ""))
		if on == "" && (leftOn == "" || rightOn == "") {
			return mcp.NewToolResultError("provide `on` (shared key) or both `left_on` and `right_on`"), nil
		}
		out := baseName(req.GetString("out", ""))
		if out == "" {
			out = "joined.csv"
		}
		env := []string{
			"J_LEFT=" + left, "J_RIGHT=" + right, "J_ON=" + on,
			"J_LON=" + leftOn, "J_RON=" + rightOn,
			"J_HOW=" + strings.ToLower(strings.TrimSpace(req.GetString("how", "inner"))),
			"J_OUT=" + out,
		}
		msg, err := runPython(ctx, root, csvJoinScript, env)
		if err != nil {
			return mcp.NewToolResultText("csv_join failed: " + err.Error() + "\n" + trunc(msg, 800)), nil
		}
		return mcp.NewToolResultText(trunc(strings.TrimSpace(msg), 4000)), nil
	}
}

const csvJoinScript = `
import os, pandas as pd
l = pd.read_csv(os.environ["J_LEFT"]); r = pd.read_csv(os.environ["J_RIGHT"])
how = os.environ.get("J_HOW") or "inner"
on = os.environ.get("J_ON","").strip()
if on:
    m = pd.merge(l, r, on=on, how=how)
else:
    m = pd.merge(l, r, left_on=os.environ["J_LON"], right_on=os.environ["J_RON"], how=how)
m.to_csv(os.environ["J_OUT"], index=False)
print(f'joined {os.environ["J_LEFT"]} + {os.environ["J_RIGHT"]} ({how}) -> {os.environ["J_OUT"]}: {len(m)} rows x {len(m.columns)} cols')
print(m.head(10).to_string(index=False))
`

// slidesGenerate builds a .pptx deck from Markdown via python-pptx.
func slidesGenerate(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		content := req.GetString("content", "")
		if strings.TrimSpace(content) == "" {
			return mcp.NewToolResultError("content (Markdown: # title, ## slides, - bullets) is required"), nil
		}
		out := baseName(req.GetString("out", "slides.pptx"))
		if !strings.HasSuffix(strings.ToLower(out), ".pptx") {
			out += ".pptx"
		}
		env := []string{"SL_MD=" + content, "SL_TITLE=" + req.GetString("title", ""), "SL_OUT=" + out}
		msg, err := runPython(ctx, root, slidesScript, env)
		if err != nil {
			return mcp.NewToolResultText("slides failed (needs python-pptx in the gateway): " + err.Error() + "\n" + trunc(msg, 600)), nil
		}
		return mcp.NewToolResultText(strings.TrimSpace(msg)), nil
	}
}

const slidesScript = `
import os
from pptx import Presentation
prs = Presentation()
def title_slide(text):
    s = prs.slides.add_slide(prs.slide_layouts[0]); s.shapes.title.text = text
body = None
title = os.environ.get("SL_TITLE","").strip()
if title: title_slide(title)
for raw in os.environ["SL_MD"].splitlines():
    line = raw.rstrip()
    if line.startswith("## "):
        s = prs.slides.add_slide(prs.slide_layouts[1])
        s.shapes.title.text = line[3:].strip()
        body = s.placeholders[1].text_frame; body.clear(); body._first = True
    elif line.startswith("# ") and not title:
        title_slide(line[2:].strip()); title = "x"
    elif line.strip()[:2] in ("- ","* ") and body is not None:
        txt = line.strip()[2:]
        if getattr(body, "_first", False): body.paragraphs[0].text = txt; body._first = False
        else: body.add_paragraph().text = txt
    elif line.strip() and body is not None:
        if getattr(body, "_first", False): body.paragraphs[0].text = line.strip(); body._first = False
        else: body.add_paragraph().text = line.strip()
prs.save(os.environ["SL_OUT"])
print(f'{len(prs.slides)} slide(s) -> {os.environ["SL_OUT"]}')
`

// pythonRun executes an inline snippet or a workspace .py file and captures output.
func pythonRun(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		code := req.GetString("code", "")
		file := baseName(req.GetString("path", ""))
		var args []string
		switch {
		case strings.TrimSpace(code) != "":
			args = []string{"-c", code}
		case file != "" && file != ".":
			args = []string{file}
		default:
			return mcp.NewToolResultError("provide `code` (a snippet) or `path` (a .py file)"), nil
		}
		if argv := strings.Fields(req.GetString("argv", "")); len(argv) > 0 {
			args = append(args, argv...)
		}
		msg, err := runInWorkspace(ctx, root, "python3", args, nil)
		out := strings.TrimSpace(msg)
		if err != nil {
			if out == "" {
				out = "(no output)"
			}
			return mcp.NewToolResultText("python exited with error: " + err.Error() + "\n--- output ---\n" + trunc(out, 8000)), nil
		}
		if out == "" {
			return mcp.NewToolResultText("(ran successfully, no output)"), nil
		}
		return mcp.NewToolResultText(trunc(out, 8000)), nil
	}
}
