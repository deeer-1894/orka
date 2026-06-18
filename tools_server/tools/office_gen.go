package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/tools_server/identity"
)

// These two tools shell out to pandoc / python+matplotlib, which are installed in
// the tools-gateway container image. They run confined to the per-user workspace.

func runInWorkspace(ctx context.Context, root, name string, args []string, extraEnv []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c := exec.CommandContext(cctx, name, args...)
	c.Dir = root
	c.Env = append(os.Environ(), append([]string{"HOME=" + root}, extraEnv...)...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// docExport converts a workspace Markdown file to HTML / DOCX / PDF via pandoc.
func docExport(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		in := filepath.Base(strings.TrimSpace(req.GetString("path", "")))
		if in == "" || in == "." {
			return mcp.NewToolResultError("path (a workspace .md file) is required"), nil
		}
		to := strings.ToLower(strings.TrimSpace(req.GetString("to", "pdf")))
		ext := map[string]string{"html": "html", "docx": "docx", "word": "docx", "pdf": "pdf"}[to]
		if ext == "" {
			return mcp.NewToolResultError("to must be one of: html, docx, pdf"), nil
		}
		out := strings.TrimSpace(req.GetString("out", ""))
		if out == "" {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + "." + ext
		}
		out = filepath.Base(out)

		args := []string{in, "-o", out, "--standalone"}
		if ext == "pdf" {
			args = append(args, "--pdf-engine=wkhtmltopdf")
		}
		if msg, err := runInWorkspace(ctx, root, "pandoc", args, nil); err != nil {
			return mcp.NewToolResultText("export failed (is pandoc available? this tool runs in the containerized gateway): " + err.Error() + "\n" + trunc(msg, 600)), nil
		}
		if fi, err := os.Stat(filepath.Join(root, out)); err == nil {
			return mcp.NewToolResultText(fmt.Sprintf("%s → %s (%s, %s) saved to your workspace", in, out, strings.ToUpper(ext), humanBytes(fi.Size()))), nil
		}
		return mcp.NewToolResultText("exported " + in + " → " + out), nil
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// chartGenerate renders a workspace CSV into a bar/line/pie PNG via matplotlib.
func chartGenerate(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root := pathsafe.UserRoot(base, identity.From(ctx).Email)
		data := filepath.Base(strings.TrimSpace(req.GetString("data", "")))
		if data == "" || data == "." {
			return mcp.NewToolResultError("data (a workspace .csv file) is required"), nil
		}
		kind := strings.ToLower(strings.TrimSpace(req.GetString("type", "bar")))
		if kind != "bar" && kind != "line" && kind != "pie" {
			kind = "bar"
		}
		out := filepath.Base(strings.TrimSpace(req.GetString("out", "chart.png")))
		if !strings.HasSuffix(strings.ToLower(out), ".png") {
			out += ".png"
		}
		agg := strings.ToLower(strings.TrimSpace(req.GetString("agg", "auto")))
		switch agg {
		case "", "auto", "sum", "avg", "mean", "count", "none":
		default:
			agg = "auto"
		}
		env := []string{
			"CHART_DATA=" + data,
			"CHART_TYPE=" + kind,
			"CHART_X=" + req.GetString("x", ""),
			"CHART_Y=" + req.GetString("y", ""),
			"CHART_AGG=" + agg,
			"CHART_TITLE=" + req.GetString("title", ""),
			"CHART_OUT=" + out,
		}
		if msg, err := runInWorkspace(ctx, root, "python3", []string{"-c", chartScript}, env); err != nil {
			return mcp.NewToolResultText("chart failed (needs python3+matplotlib in the containerized gateway): " + err.Error() + "\n" + trunc(msg, 600)), nil
		}
		return mcp.NewToolResultText("chart (" + kind + ") saved to " + out), nil
	}
}

// chartScript reads config from env so we never have to quote argv. It plots the
// first two columns by default, or the named x/y columns. When the x column has
// repeated labels (e.g. 1000 sales rows over 5 products) it aggregates y per
// label so the chart stays legible instead of drawing one bar per row. CJK fonts
// are registered so Chinese/Japanese/Korean labels render instead of tofu boxes.
const chartScript = `
import csv, os
import matplotlib
matplotlib.use("Agg")
from matplotlib import font_manager
import matplotlib.pyplot as plt

# Register any bundled CJK font so non-Latin labels render.
for fp in ("/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
           "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.otf",
           "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc"):
    if os.path.exists(fp):
        try:
            font_manager.fontManager.addfont(fp)
            plt.rcParams["font.sans-serif"] = [font_manager.FontProperties(fname=fp).get_name()]
        except Exception: pass
        break
plt.rcParams["axes.unicode_minus"] = False

with open(os.environ["CHART_DATA"], newline="") as f:
    rows = list(csv.reader(f))
header, rows = rows[0], rows[1:]
def col(name, default):
    if name and name in header: return header.index(name)
    return default
xi = col(os.environ.get("CHART_X",""), 0)
yi = col(os.environ.get("CHART_Y",""), 1 if len(header) > 1 else 0)
m = max(xi, yi)
pairs = []
for r in rows:
    if len(r) <= m: continue
    try: v = float(r[yi])
    except: v = 0.0
    pairs.append((r[xi], v))

agg = os.environ.get("CHART_AGG","auto")
labels = [p[0] for p in pairs]
# auto: aggregate only when labels repeat (categorical), keep raw otherwise.
if agg == "auto":
    agg = "sum" if len(set(labels)) < len(labels) else "none"
if agg in ("sum","avg","mean","count"):
    order, sums, counts = [], {}, {}
    for lab, v in pairs:
        if lab not in sums: order.append(lab); sums[lab] = 0.0; counts[lab] = 0
        sums[lab] += v; counts[lab] += 1
    if agg == "count":
        labels, vals = order, [counts[l] for l in order]
    elif agg in ("avg","mean"):
        labels, vals = order, [sums[l]/counts[l] for l in order]
    else:
        labels, vals = order, [sums[l] for l in order]
else:
    vals = [p[1] for p in pairs]

kind = os.environ.get("CHART_TYPE","bar")
plt.figure(figsize=(9,5))
if kind == "line": plt.plot(labels, vals, marker="o")
elif kind == "pie": plt.pie(vals, labels=labels, autopct="%1.1f%%")
else: plt.bar(labels, vals)
if kind != "pie":
    plt.xticks(rotation=30, ha="right"); plt.ylabel(header[yi]); plt.xlabel(header[xi])
plt.title(os.environ.get("CHART_TITLE","") or header[yi])
plt.tight_layout()
plt.savefig(os.environ["CHART_OUT"], dpi=120)
print("saved", os.environ["CHART_OUT"])
`
