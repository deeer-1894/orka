package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var weekdaysZH = map[time.Weekday]string{
	time.Sunday: "周日", time.Monday: "周一", time.Tuesday: "周二", time.Wednesday: "周三",
	time.Thursday: "周四", time.Friday: "周五", time.Saturday: "周六",
}

// currentTime returns the current date/time. The model otherwise has no idea
// what "today" is — essential for any time-relative request.
func currentTime() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tz := req.GetString("timezone", "Asia/Shanghai")
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc, tz = time.UTC, "UTC"
		}
		now := time.Now().In(loc)
		out := fmt.Sprintf("Current time: %s %s (%s)",
			now.Format("2006-01-02 15:04:05"), weekdaysZH[now.Weekday()], tz)
		return mcp.NewToolResultText(out), nil
	}
}
