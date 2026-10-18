package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/security"
)

func TestCatalogWithoutConversationNeverGrantsExecution(t *testing.T) {
	t.Setenv("CODE_EXECUTION", "1")
	base := filepath.Join(t.TempDir(), "not-created")
	endpoint := startGateway(t, Config{Secret: testSecret, BaseStorage: base, Blacklist: []string{"aio_echo"}})
	claims := security.NewToken("catalog-fixture@example.test", []string{"tools:catalog"}, time.Minute)
	// No conversation: this credential may discover metadata, never execute.
	token, err := security.Sign(claims, []byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	client := connect(t, endpoint, map[string]string{"X-Orka-Token": token})
	defer client.Close()
	names := listNames(t, client)
	for _, name := range []string{"file_write", "python", "shell", "current_time"} {
		if !names[name] {
			t.Errorf("catalog omitted %s", name)
		}
	}
	if names["aio_echo"] {
		t.Error("catalog exposed blacklisted tool")
	}
	for name := range names {
		result, message := callText(t, client, name, map[string]any{"expression": "1+1", "path": "unexpected.txt", "content": "no", "command": "printf no", "code": "print('no')"})
		if !result.IsError || !strings.Contains(message, "catalog") {
			t.Errorf("catalog token executed %s", name)
		}
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("metadata listing created a workspace")
	}
}
