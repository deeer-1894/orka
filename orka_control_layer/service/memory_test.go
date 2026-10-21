package service

import (
	"context"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

type fakeMemories struct{ saved []db.Memory }

func (f *fakeMemories) AddMemory(_ context.Context, m *db.Memory) (bool, error) {
	f.saved = append(f.saved, *m)
	return true, nil
}

func TestPersonalContextOnlyRemembersWhileAutoMemoryIsOn(t *testing.T) {
	store := &fakeMemories{}
	off := &runPersonal{Personalization: db.Personalization{AboutMe: "产品经理", ReplyStyle: "简洁"}}
	if p := off.prompt(); !strings.Contains(p, "[关于用户]\n产品经理") || !strings.Contains(p, "[回复偏好]\n简洁") || strings.Contains(p, "remember") {
		t.Fatalf("explicit preferences without memory:\n%s", p)
	}
	if tools := off.tools(store, ChatRunRequest{UserEmail: "a@x"}, ""); len(tools) != 0 {
		t.Fatal("no remember tool while auto memory is off")
	}

	on := &runPersonal{Personalization: db.Personalization{AutoMemory: true}, memories: []db.Memory{{Content: "偏好表格"}}}
	if p := on.prompt(); !strings.Contains(p, "- 偏好表格") || !strings.Contains(p, "remember") {
		t.Fatalf("memories and the remember hint:\n%s", p)
	}
	tools := on.tools(store, ChatRunRequest{UserEmail: "a@x", ConversationID: "c1"}, "prj_1")
	if len(tools) != 1 || tools[0].Name() != rememberToolName {
		t.Fatalf("tools = %v", tools)
	}
	if _, err := tools[0].Invoke(context.Background(), map[string]any{"content": "我的密码是 123"}); err == nil {
		t.Fatal("a secret must never be saved")
	}
	if _, err := tools[0].Invoke(context.Background(), map[string]any{"content": "周报用表格", "scope": "project"}); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.saved[0].ProjectID != "prj_1" || store.saved[0].SourceConversationID != "c1" {
		t.Fatalf("saved %+v", store.saved)
	}
	var nilPersonal *runPersonal
	if nilPersonal.prompt() != "" || withPromptBlock("base", "") != "base" {
		t.Fatal("nothing to add leaves the prompt alone")
	}
}
