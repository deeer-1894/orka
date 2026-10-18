package tools

import (
	"context"
	"fmt"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio/contracttest"
	"github.com/orka-oss/tools_server/identity"
	"testing"
)

func TestSharedWriteContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) (string, contracttest.Writer) {
		base := t.TempDir()
		root, err := pathsafe.EnsureSession(base, "fixture@example.test", "contract")
		if err != nil {
			t.Fatal(err)
		}
		ctx := identity.With(context.Background(), identity.Identity{Email: "fixture@example.test", ConversationID: "contract"})
		return root, func(args map[string]any) error {
			result, err := fileWrite(base)(ctx, callWith(args))
			if err != nil {
				return err
			}
			if result.IsError {
				return fmt.Errorf("%s", writeGuardText(result))
			}
			return nil
		}
	})
}
