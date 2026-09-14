package api

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/modelsettings"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"strings"
	"testing"
)

func settingsAPI(t *testing.T) *API {
	cfg := &config.Config{}
	cfg.Storage.BaseStoragePath = t.TempDir()
	cfg.LLM.Model = "deployment"
	return &API{Chat: service.NewChatService(cfg, nil, nil, nil, nil, nil, nil)}
}
func settingsRequest(email, body string) *app.RequestContext {
	c := app.NewContext(0)
	if email != "" {
		c.Set("email", email)
	}
	c.Request.SetBodyString(body)
	return c
}
func TestModelSettingsAPIContract(t *testing.T) {
	a := settingsAPI(t)
	c := settingsRequest("alice", `{"provider":"custom","base_url":"http://localhost:8080/v1","api_key":"secret-value","models":["mine","manual"],"enabled":true,"user_email":"bob"}`)
	a.SaveModelSettings(context.Background(), c)
	if c.Response.StatusCode() != 200 {
		t.Fatal(string(c.Response.Body()))
	}
	body := string(c.Response.Body())
	if strings.Contains(body, `"model":`) || strings.Contains(body, `"mini_model":`) || strings.Contains(body, "secret-value") || strings.Contains(body, `"api_key":`) || !strings.Contains(body, `"api_key_set":true`) {
		t.Fatal(body)
	}
	c = settingsRequest("alice", "")
	a.GetModelSettings(context.Background(), c)
	if !strings.Contains(string(c.Response.Body()), `"models":["mine","manual"]`) {
		t.Fatal(string(c.Response.Body()))
	}
	c = settingsRequest("bob", "")
	a.GetModelSettings(context.Background(), c)
	if strings.Contains(string(c.Response.Body()), `"models":["mine","manual"]`) {
		t.Fatal("cross user leak")
	}
	c = settingsRequest("alice", "")
	a.ListModels(context.Background(), c)
	body = string(c.Response.Body())
	if !strings.Contains(body, "mine") || !strings.Contains(body, "manual") || strings.Contains(body, "deployment") {
		t.Fatal(body)
	}
	c = settingsRequest("alice", `{"enabled":false}`)
	a.SaveModelSettings(context.Background(), c)
	c = settingsRequest("alice", "")
	a.ListModels(context.Background(), c)
	if !strings.Contains(string(c.Response.Body()), "deployment") {
		t.Fatal(string(c.Response.Body()))
	}
	c = settingsRequest("alice", "")
	a.GetModelSettings(context.Background(), c)
	var response struct {
		Data modelsettings.PublicConfig `json:"data"`
	}
	json.Unmarshal(c.Response.Body(), &response)
	if response.Data.Enabled || !response.Data.APIKeySet {
		t.Fatal("disabled config not retained")
	}
}
func TestModelSettingsRequireIdentity(t *testing.T) {
	a := settingsAPI(t)
	for _, handler := range []func(context.Context, *app.RequestContext){a.GetModelSettings, a.SaveModelSettings, a.DiscoverModels, a.ListModels} {
		c := settingsRequest("", `{"user_email":"alice"}`)
		handler(context.Background(), c)
		if c.Response.StatusCode() != 401 {
			t.Fatalf("got %d", c.Response.StatusCode())
		}
	}
}

func TestModelSettingsListOnlyContract(t *testing.T) {
	a := settingsAPI(t)
	a.Chat.Cfg.LLM.MiniModel = "legacy-mini"
	a.Chat.Cfg.LLM.Models = []string{"other", "deployment"}
	c := settingsRequest("alice", "")
	a.ListModels(context.Background(), c)
	var list struct {
		Data []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(c.Response.Body(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 3 || list.Data[0]["version"] != "auto" || list.Data[0]["label"] != "Auto" || list.Data[0]["hint"] != "使用列表中的第一个模型" || list.Data[1]["version"] != "deployment" || list.Data[2]["version"] != "other" {
		t.Fatal(list.Data)
	}
	for _, body := range []string{`{"base_url":"http://localhost/v1","models":[],"enabled":true}`, `{"base_url":"http://localhost/v1","models":[" "],"enabled":true}`} {
		c = settingsRequest("alice", body)
		a.SaveModelSettings(context.Background(), c)
		if c.Response.StatusCode() != 400 {
			t.Fatal("empty models accepted", string(c.Response.Body()))
		}
	}
	c = settingsRequest("alice", `{"base_url":"http://localhost/v1","models":["second","first","second"],"enabled":true}`)
	a.SaveModelSettings(context.Background(), c)
	if c.Response.StatusCode() != 200 {
		t.Fatal(string(c.Response.Body()))
	}
	c = settingsRequest("alice", "")
	a.ListModels(context.Background(), c)
	json.Unmarshal(c.Response.Body(), &list)
	if len(list.Data) != 3 || list.Data[1]["version"] != "second" || list.Data[2]["version"] != "first" {
		t.Fatal(list.Data)
	}
}

func TestFollowupsBindsManualSelection(t *testing.T) {
	a := settingsAPI(t)
	a.Chat.Cfg.LLM.Models = []string{"manual"}
	mock := llm.NewMock(llm.Response{Content: `["Next?"]`, FinishReason: "stop"})
	a.Chat.Main = mock
	// A prior Run provides the opaque profile through normal message metadata.
	var profile string
	a.Chat.ToolsFor = func(context.Context, service.ChatRunRequest) ([]agent.BaseTool, func(), error) { return nil, nil, nil }
	a.Chat.Msg = message_utils.New(nil, 1, nil)
	a.Chat.DisableSummary = true
	a.Chat.DisableFastPath = true
	a.Chat.Run(context.Background(), service.ChatRunRequest{UserEmail: "alice", ConversationID: "profile", Message: "Q", SelectedVersion: "manual"}, func(m messages.Message) {
		if m.Meta.ModelProfile != "" {
			profile = m.Meta.ModelProfile
		}
	})
	mock = llm.NewMock(llm.Response{Content: `["Next?"]`, FinishReason: "stop"})
	a.Chat.Main = mock
	payload, _ := json.Marshal(map[string]string{"prompt": "Q", "answer": "A", "selected_version": "manual", "model_profile": profile})
	c := settingsRequest("alice", string(payload))
	a.Followups(context.Background(), c)
	if c.Response.StatusCode() != 200 || mock.Calls() != 1 || mock.Requests[0].Model != "manual" {
		t.Fatal(string(c.Response.Body()), mock.Calls())
	}
	payload, _ = json.Marshal(map[string]string{"prompt": "Q", "answer": "A", "selected_version": "removed", "model_profile": profile})
	c = settingsRequest("alice", string(payload))
	a.Followups(context.Background(), c)
	if c.Response.StatusCode() != 400 || mock.Calls() != 1 {
		t.Fatal("invalid followup selection called provider")
	}
}
