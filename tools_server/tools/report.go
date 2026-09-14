package tools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/reporting"
	"github.com/orka-oss/tools_server/identity"
)

func renderReport(base string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fail := func(err error) (*mcp.CallToolResult, error) {
			if cancelled := ctx.Err(); cancelled != nil {
				return nil, cancelled
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		specPath := pathArg(req)
		if !strings.HasSuffix(specPath, ".report.json") {
			return mcp.NewToolResultError("path must name a workspace-relative .report.json specification"), nil
		}
		user := identity.From(ctx)
		rootPath, err := pathsafe.SessionRoot(base, user.Email, user.ConversationID)
		if err != nil {
			return fail(err)
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			return fail(err)
		}
		defer root.Close()
		raw, err := reporting.ReadSpec(root.FS(), specPath)
		if err != nil {
			return fail(err)
		}
		rendered, err := reporting.Render(ctx, root.FS(), raw)
		if err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		backupBeforeWrite(base, user.Email, rendered.Output, user.ConversationID)
		if err = writeRenderedReport(ctx, root, rendered.Output, rendered.Content); err != nil {
			return fail(err)
		}
		result := struct {
			reporting.Result
			Spec  string `json:"spec"`
			Scope string `json:"scope"`
		}{rendered, specPath, "Bound placeholders are computed from the current CSVs. This does not verify literal prose, source suitability or business formulas. Declare BOTH this .report.json spec and the .md output in update_plan.outputs; check_delivery then recomputes bindings and rejects stale/edited output. Rerender after source changes; regenerate any manifest hashes afterwards."}
		payload, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(payload)), nil
	}
}

// Replace atomically within the pinned session root. A symlink at the output
// is replaced as a directory entry, never followed to overwrite its target.
func writeRenderedReport(ctx context.Context, root *os.Root, output, content string) error {
	dir := path.Dir(output)
	if err := root.MkdirAll(dir, pathsafe.WorkspaceDirMode); err != nil {
		return err
	}
	temp := path.Join(dir, ".report-"+rand.Text()+".tmp")
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, pathsafe.WorkspaceFileMode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temp, output); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}
