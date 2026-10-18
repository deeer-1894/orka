package service

import (
	"context"
	"fmt"
)

// Unknown names are model mistakes, not transport failures. Return a normal
// failure receipt so the agent can choose from its real scoped catalog. Never
// guess an alias or execute the rejected arguments: that could bypass tool gates.
func unknownToolReceipt(ctx context.Context, name, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("tool error: unknown tool %q; not executed. Use the exact names and schemas in your available tools. Use find_tools to discover a required capability when that tool is available; do not invent names or retry this rejected call unchanged. Continue from existing results.", trunc(name, 120)), nil
}
