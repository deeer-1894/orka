package messages

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestConstructorsFillBaseFields(t *testing.T) {
	meta := Meta{ConversationID: "c1", TraceID: "t1"}
	m := Chat(RoleAssistant, "hi", meta)
	if m.ID == "" || m.Timestamp == 0 {
		t.Fatalf("id/ts not filled: %+v", m)
	}
	if m.Type != EventChat || m.Role != RoleAssistant || m.Content != "hi" {
		t.Fatalf("unexpected chat message: %+v", m)
	}
	if m.Meta.TraceID != "t1" {
		t.Fatalf("meta not carried: %+v", m.Meta)
	}
}

func TestClarifyPayloadRoundTrip(t *testing.T) {
	c := ClarifyMessage{Question: "which?", Options: []string{"a", "b"}, ResumeKey: "cp1"}
	m := Clarify(c, Meta{})
	b, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got Message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != EventClarify {
		t.Fatalf("type = %s", got.Type)
	}
}

func TestSSEFraming(t *testing.T) {
	m := Heartbeat(Meta{})
	frame, err := m.SSE()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(frame, []byte("data: ")) {
		t.Fatalf("missing data prefix: %q", frame)
	}
	if !bytes.HasSuffix(frame, []byte("\n\n")) {
		t.Fatalf("missing blank-line terminator: %q", frame)
	}
}

func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
	}
}
