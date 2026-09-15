package filesystem

import (
	"context"
	"github.com/orka-oss/orka_core/workspaceio/contracttest"
	"testing"
)

func TestSharedWriteContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) (string, contracttest.Writer) {
		root := t.TempDir()
		tool := New(root)[1]
		return root, func(args map[string]any) error { _, err := tool.Invoke(context.Background(), args); return err }
	})
}
