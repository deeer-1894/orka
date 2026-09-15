package api

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/modelsettings"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"io"
	"net/http"
	"strings"
	"testing"
)

func settingsAPI(t *testing.T) *API {
	cfg := &config.Config{}
	cfg.Storage.BaseStoragePath = t.TempDir()
	cfg.LLM.Model = "deployment"
	chat := service.NewChatService(cfg, nil, nil, nil, nil, nil)
	chat.UsageLedger = &apiBudgetLedger{}
	return &API{Chat: chat}
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
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("trusted stored selection", func(mt *mtest.T) {
		t := mt.T
		a := settingsAPI(t)
		a.Chat.Cfg.LLM.Models = []string{"manual"}
		mock := llm.NewMock(llm.Response{Content: `["Next?"]`, FinishReason: "stop"})
		a.Chat.Client = llm.NewAccounted(mock)
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
		a.Chat.Client = llm.NewAccounted(mock)
		a.Chat.Msg.Store = &db.Storage{Runs: mt.Coll}
		stored := bson.D{{Key: "run_id", Value: "saved"}, {Key: "owner_email", Value: "alice"}, {Key: "conversation_id", Value: "profile"}, {Key: "status", Value: db.RunDone}, {Key: "model", Value: "manual"}, {Key: "prompt", Value: "stored question"}, {Key: "output", Value: "stored answer"}}
		readRun := func() {
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, stored))
		}
		stored[3].Value = db.RunRunning
		readRun()
		payload, _ := json.Marshal(map[string]string{"conversation_id": "profile", "run_id": "saved", "prompt": "UNTRUSTED QUESTION", "answer": "UNTRUSTED ANSWER", "selected_version": "manual", "model_profile": profile})
		c := settingsRequest("alice", string(payload))
		a.Followups(context.Background(), c)
		if c.Response.StatusCode() != 409 || mock.Calls() != 0 {
			t.Fatal("running run paid for followups")
		}
		stored[3].Value = db.RunDone
		readRun()
		c = settingsRequest("alice", string(payload))
		a.Followups(context.Background(), c)
		if c.Response.StatusCode() != 200 || mock.Calls() != 1 || mock.Requests[0].Model != "manual" {
			t.Fatal(string(c.Response.Body()), mock.Calls())
		}
		if !strings.Contains(mock.Requests[0].Messages[1].Content, "stored answer") || strings.Contains(mock.Requests[0].Messages[1].Content, "UNTRUSTED") {
			t.Fatal("used browser text instead of stored run")
		}
		readRun()
		payload, _ = json.Marshal(map[string]string{"conversation_id": "profile", "run_id": "saved", "prompt": "UNTRUSTED QUESTION", "answer": "UNTRUSTED ANSWER", "selected_version": "removed", "model_profile": profile})
		c = settingsRequest("alice", string(payload))
		a.Followups(context.Background(), c)
		if c.Response.StatusCode() != 200 || mock.Calls() != 1 {
			t.Fatal("legacy selected_version changed stored selection or bypassed deduplication")
		}
		account, err := a.Chat.UsageLedger.Load(context.Background(), "alice")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range account.Entries {
			if entry.Source != "followups" {
				continue
			}
			count++
			if entry.RelatedConversationID != "profile" || entry.RelatedRunID != "saved" || entry.RunID == "saved" {
				t.Fatalf("missing independent aux association: %+v", entry)
			}
			snapshot, err := a.Chat.RunBudgetSnapshot(context.Background(), "alice", entry.RunID)
			if err != nil || snapshot.RelatedRunID != "saved" || snapshot.RelatedConversationID != "profile" {
				t.Fatalf("missing durable aux projection: %+v %v", snapshot, err)
			}
		}
		if count != 1 {
			t.Fatalf("followup ledger attempts=%d want 1", count)
		}
	})
}

type settingsDiscoveryTransport func(*http.Request) (*http.Response, error)

func (f settingsDiscoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiscoverModelsMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, base, body, source string
		status                   int
		want                     []string
	}{
		{"plan preset", "https://ark.cn-beijing.volces.com/api/plan/v3", "provider-private-body", "preset", 404, []string{"doubao-seed-2.1-turbo", "doubao-seed-evolving", "doubao-seed-2.0-lite", "minimax-m3", "glm-5.3", "glm-latest", "glm-5.3-flash", "deepseek-v4-flash", "deepseek-v4-pro", "kimi-k2.7-code", "kimi-k3", "ark-code-latest"}},
		{"coding preset", "https://ark.cn-beijing.volces.com/api/coding/v3", "provider-private-body", "preset", 405, []string{"doubao-seed-2.1-turbo", "doubao-seed-evolving", "doubao-seed-2.0-lite", "minimax-m3", "glm-5.3", "glm-latest", "glm-5.3-flash", "deepseek-v4-flash", "deepseek-v4-pro", "kimi-k2.7-code", "kimi-k3", "ark-code-latest"}},
		{"official remote wins", "https://ark.cn-beijing.volces.com/api/plan/v3", `{"data":[{"id":"z"},{"id":"a"},{"id":"z"}]}`, "remote", 200, []string{"a", "z"}},
		{"custom remote", "https://custom.test/v1", `{"data":[]}`, "remote", 200, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := http.DefaultTransport
			calls := 0
			http.DefaultTransport = settingsDiscoveryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.base+"/models" || r.Header.Get("Authorization") != "Bearer fake-test-key" {
					t.Fatalf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = prior })
			a := settingsAPI(t)
			payload, _ := json.Marshal(map[string]string{"base_url": tc.base, "api_key": "fake-test-key"})
			c := settingsRequest("alice", string(payload))
			a.DiscoverModels(context.Background(), c)
			if c.Response.StatusCode() != 200 {
				t.Fatal(string(c.Response.Body()))
			}
			var response struct {
				Data struct {
					Models []string `json:"models"`
					Source string   `json:"source"`
					Notice string   `json:"notice"`
				} `json:"data"`
			}
			if err := json.Unmarshal(c.Response.Body(), &response); err != nil {
				t.Fatal(err)
			}
			got := response.Data
			if got.Source != tc.source || strings.Join(got.Models, ",") != strings.Join(tc.want, ",") || got.Models == nil || calls != 1 {
				t.Fatalf("unexpected metadata: %+v calls=%d", got, calls)
			}
			if tc.source == "preset" && (!strings.Contains(got.Notice, "列表不代表密钥或套餐权限已验证") || !strings.Contains(got.Notice, "预设")) {
				t.Fatalf("preset must disclose unverified candidates: %q", got.Notice)
			}
			if tc.source == "remote" && got.Notice != "" {
				t.Fatalf("remote carried fallback notice: %q", got.Notice)
			}
			body := string(c.Response.Body())
			if strings.Contains(body, "fake-test-key") || strings.Contains(body, "provider-private-body") {
				t.Fatal("response exposed provider data")
			}
			if c.Response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
}

func TestNamedProfilesAPIContract(t *testing.T) {
	a := settingsAPI(t)
	c := settingsRequest("alice", `{"profiles":[{"id":"work","name":"Work","protocol":"openai-compatible","base_url":"http://localhost/v1","api_key":"private-key","models":["first","manual"],"enabled":true,"verified":{"first":{"vision":{"verified":true}}}}],"active_profile_id":"work"}`)
	a.SaveModelProfiles(context.Background(), c)
	if c.Response.StatusCode() != 200 {
		t.Fatal(string(c.Response.Body()))
	}
	body := string(c.Response.Body())
	if strings.Contains(body, "private-key") || strings.Contains(body, `"api_key":`) || strings.Contains(body, `"verified":true`) {
		t.Fatal("secret/forged verification in response")
	}
	c = settingsRequest("alice", "")
	a.GetModelProfiles(context.Background(), c)
	if !strings.Contains(string(c.Response.Body()), `"active_profile_id":"work"`) {
		t.Fatal("missing active profile")
	}
	c = settingsRequest("alice", "")
	a.GetModelSettings(context.Background(), c)
	if !strings.Contains(string(c.Response.Body()), `"models":["first","manual"]`) {
		t.Fatal("legacy doesn't read active")
	}
	for _, h := range []func(context.Context, *app.RequestContext){a.GetModelProfiles, a.SaveModelProfiles, a.ProbeModelProfile} {
		c = settingsRequest("", `{}`)
		h(context.Background(), c)
		if c.Response.StatusCode() != 401 {
			t.Fatal("profile handler allows anonymous")
		}
	}
	c = settingsRequest("alice", `{"profile_id":"work","model":"first","capabilities":[]}`)
	a.ProbeModelProfile(context.Background(), c)
	if c.Response.StatusCode() != 400 {
		t.Fatal("unrequested probe accepted")
	}
}
