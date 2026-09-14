package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
)

func TestAttachmentPreviewDoesNotBecomeImmutableHumanRequest(t *testing.T) {
	svc, _ := testService(t, llm.NewMock())
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	root, err := pathsafe.EnsureSession(svc.Cfg.Storage.BaseStoragePath, "owner", "conv")
	if err != nil {
		t.Fatal(err)
	}
	data := "ticket_id,priority,note\n" + strings.Repeat("T1,P0,示例记录\n", 3000)
	if err = os.WriteFile(filepath.Join(root, "tickets.csv"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	req := ChatRunRequest{UserEmail: "owner", ConversationID: "conv", Message: "Audit tickets.csv; use UTC and strictly greater than 4 hours.", FileIDs: []string{"tickets.csv"}}
	seed := toEinoMessages(svc.withAttachments(context.Background(), []messages.Message{humanChat(req.Message, messages.Meta{})}, req, messages.Meta{}))
	human, attachments := 0, 0
	for _, m := range seed {
		if isHumanRequest(m) {
			human++
			if m.Content != req.Message {
				t.Error("attachment bytes retained as immutable user requirements")
			}
		}
		if isRuntimeInput(m) && strings.Contains(m.Content, "tickets.csv") {
			attachments++
			if len(m.Content) > 4096 || !utf8.ValidString(m.Content) || !strings.Contains(m.Content, "ticket_id,priority,note") {
				t.Error("missing or oversized CSV sample")
			}
		}
	}
	if human != 1 || attachments != 1 {
		t.Fatalf("human=%d attachments=%d", human, attachments)
	}
	compressed, err := finalizeSummary(context.Background(), seed, schema.AssistantMessage("Input remains at tickets.csv.", nil))
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		humans := 0
		for _, m := range compressed {
			if strings.Contains(m.Content, "T1,P0,") {
				t.Error("compression retained raw CSV preview")
			}
			if isHumanRequest(m) {
				humans++
				if m.Content != req.Message {
					t.Error("compression changed human requirements")
				}
			}
		}
		if humans != 1 {
			t.Fatalf("summary pass %d: human requests=%d", pass, humans)
		}
		compressed, err = finalizeSummary(context.Background(), compressed, schema.AssistantMessage("Input remains at tickets.csv.", nil))
		if err != nil {
			t.Fatal(err)
		}
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "tickets.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != data {
		t.Error("preview modified original data")
	}
}

func TestAttachmentPreviewRetainsSmallTextAndBoundsCombinedText(t *testing.T) {
	svc, _ := testService(t, llm.NewMock())
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	root, err := pathsafe.EnsureSession(svc.Cfg.Storage.BaseStoragePath, "owner", "conv")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "note.txt"), []byte("precise short requirements"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if err = os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat("x", 30000)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := svc.processAttachments(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "conv", FileIDs: []string{"note.txt", "one.txt", "two.txt"}})
	if !strings.Contains(got, "precise short requirements") || !strings.Contains(got, "two.txt") {
		t.Error("attachment or path disappeared")
	}
	if len(got) > 22000 {
		t.Fatalf("combined text unbounded: %d", len(got))
	}
}

func TestAttachmentPreviewUTF8BoundaryAndExhaustedBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unicode.csv")
	original := strings.Repeat("x", maxTabularPreview-1) + "中尾"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := attachmentPreview(path, maxTabularPreview)
	if err != nil || !truncated || !utf8.ValidString(got) || got != strings.Repeat("x", maxTabularPreview-1) {
		t.Fatalf("UTF8 boundary: size=%d truncated=%v err=%v", len(got), truncated, err)
	}
	got, truncated, err = attachmentPreview(path, 0)
	if err != nil || !truncated || got != "" {
		t.Fatalf("exhausted budget: %q %v %v", got, truncated, err)
	}
}
