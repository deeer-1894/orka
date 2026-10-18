package toolsmanager

import (
	"context"
	"github.com/orka-oss/orka_core/agent"
	"testing"
)

type fakeTool string

func (t fakeTool) Name() string                                           { return string(t) }
func (t fakeTool) Description() string                                    { return "test" }
func (t fakeTool) Schema() map[string]any                                 { return nil }
func (t fakeTool) Invoke(context.Context, map[string]any) (string, error) { return "", nil }
func TestDuplicateToolNamesRejected(t *testing.T) {
	_, err := New([]agent.BaseTool{fakeTool("file_read"), fakeTool("file_read")}).GetTools(context.Background())
	if err == nil {
		t.Fatal("duplicate tool accepted")
	}
}
