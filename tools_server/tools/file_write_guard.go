package tools

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Validate before path creation and version backup. Historical context may be
// replayed by a model; absent content must never silently become an empty file.
func fileWriteContent(req mcp.CallToolRequest) (string, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be an explicit string; historical calls may omit it. Use file_read to retrieve existing content before editing; no file was changed")
	}
	trimmed := strings.TrimSpace(content)
	// Legacy journals still contain whole-value compression pointers. Reject
	// only a whole pointer; ordinary documents quoting these tags remain valid.
	for _, tag := range []string{"persisted-arg", "persisted-output"} {
		if strings.HasPrefix(trimmed, "<"+tag+">") && strings.HasSuffix(trimmed, "</"+tag+">") {
			return "", fmt.Errorf("content is a context compression pointer, not file data. Use file_read to recover the content and retry with actual text; no file was changed")
		}
	}
	return content, nil
}

// The omitted mode intentionally means create, including for legacy clients.
// Validate before resolving paths, creating directories, or backing up files.
func fileWriteMode(req mcp.CallToolRequest) (string, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	value, present := args["mode"]
	if !present {
		return "create", nil
	}
	mode, ok := value.(string)
	if ok && (mode == "create" || mode == "replace" || mode == "append") {
		return mode, nil
	}
	return "", fmt.Errorf("mode must be create, replace, or append; omit it for create. No file was changed")
}
