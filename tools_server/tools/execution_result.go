package tools

import (
	"encoding/json"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/orka-oss/tools_server/runner"
)

func executionResult(out runner.Outcome) *mcp.CallToolResult {
	data, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError("cannot encode execution outcome")
	}
	if !out.OK {
		return mcp.NewToolResultError(string(data))
	}
	return mcp.NewToolResultText(string(data))
}
func executionFailure(err error) *mcp.CallToolResult {
	return executionResult(runner.Outcome{ExitCode: -1, Error: err.Error()})
}
