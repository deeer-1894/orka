package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/scheduled_task"
)

type fakeTaskNotifyStore struct {
	task  *db.TaskMeta
	mu    sync.Mutex
	saved []db.Notification
}

func (f *fakeTaskNotifyStore) GetTask(_ context.Context, id string) (*db.TaskMeta, error) {
	if f.task == nil || f.task.TaskID != id {
		return nil, db.ErrNotFound
	}
	return f.task, nil
}

func (f *fakeTaskNotifyStore) CreateNotification(_ context.Context, n *db.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, *n)
	return nil
}

func notifyTask(webhook string) *db.TaskMeta {
	return &db.TaskMeta{
		TaskID: "task-1", OwnerEmail: "owner@example.test", ConversationID: "conv-1",
		Variables: map[string]any{"title": "每日简报", "prompt_template": "整理新闻"}, NotifyWebhook: webhook,
	}
}

func TestTaskRunCreatesInAppNotification(t *testing.T) {
	for _, tc := range []struct {
		status, answer, errStr string
		wantTitle, wantBody    string
	}{
		{db.RunDone, "\n第一条要点\n第二条要点\n\n第三条要点\n第四条不该出现", "", "任务已完成：每日简报", "第一条要点\n第二条要点\n第三条要点"},
		{db.RunPartial, "只完成了一半", "", "任务部分完成：每日简报", "只完成了一半"},
		{db.RunFailed, "半截回答", "model unavailable", "任务失败：每日简报", "model unavailable"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			store := &fakeTaskNotifyStore{task: notifyTask("")}
			var events []string
			n := &TaskNotifier{Store: store, OnEvent: func(email, kind string) { events = append(events, email+":"+kind) }}
			n.RunFinished(context.Background(), TaskRunResult{
				TaskID: "task-1", RunID: "run-9", OwnerEmail: "owner@example.test", Status: tc.status, Answer: tc.answer, Error: tc.errStr,
			})
			if len(store.saved) != 1 {
				t.Fatalf("notifications=%d", len(store.saved))
			}
			got := store.saved[0]
			if got.OwnerEmail != "owner@example.test" || got.Kind != "task_run" || got.RunID != "run-9" || got.ConversationID != "conv-1" {
				t.Fatalf("notification=%+v", got)
			}
			if got.Title != tc.wantTitle || got.Body != tc.wantBody {
				t.Fatalf("title=%q body=%q", got.Title, got.Body)
			}
			if len(events) != 1 || events[0] != "owner@example.test:notification" {
				t.Fatalf("events=%v", events)
			}
		})
	}
}

func TestTaskRunNotificationIgnoresForeignOrMissingTask(t *testing.T) {
	store := &fakeTaskNotifyStore{task: notifyTask("")}
	n := &TaskNotifier{Store: store}
	n.RunFinished(context.Background(), TaskRunResult{TaskID: "task-1", OwnerEmail: "intruder@example.test", Status: db.RunDone})
	n.RunFinished(context.Background(), TaskRunResult{TaskID: "missing", OwnerEmail: "owner@example.test", Status: db.RunDone})
	if len(store.saved) != 0 {
		t.Fatalf("notified for a task the run does not own: %+v", store.saved)
	}
}

func TestTaskRunDeliversOutboundSummary(t *testing.T) {
	var got map[string]any
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	store := &fakeTaskNotifyStore{task: notifyTask(srv.URL + "/hooks/abc")}
	n := &TaskNotifier{Store: store, Client: srv.Client(), PublicURL: "https://orka.example.com/"}
	n.RunFinished(context.Background(), TaskRunResult{
		TaskID: "task-1", RunID: "run-1", OwnerEmail: "owner@example.test", Status: db.RunDone, Answer: "今日要点\n一、模型发布",
	})
	if contentType != "application/json" {
		t.Fatalf("content-type=%q", contentType)
	}
	text, _ := got["text"].(string)
	for _, want := range []string{"【Orka 任务已完成】每日简报", "今日要点\n一、模型发布", "查看对话：https://orka.example.com/?c=conv-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary %q missing %q", text, want)
		}
	}
	if len(store.saved) != 1 {
		t.Fatalf("in-app notification missing alongside delivery")
	}
}

func TestTaskSummaryPayloadMatchesReceiver(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://open.feishu.cn/open-apis/bot/v2/hook/x", `{"content":{"text":"hi"},"msg_type":"text"}`},
		{"https://open.larksuite.com/open-apis/bot/v2/hook/x", `{"content":{"text":"hi"},"msg_type":"text"}`},
		{"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x", `{"msgtype":"text","text":{"content":"hi"}}`},
		{"https://oapi.dingtalk.com/robot/send?access_token=x", `{"msgtype":"text","text":{"content":"hi"}}`},
		{"https://hooks.slack.com/services/T/B/x", `{"text":"hi"}`},
	} {
		raw, err := taskSummaryPayload(tc.url, "hi")
		if err != nil || string(raw) != tc.want {
			t.Fatalf("%s: %s %v", tc.url, raw, err)
		}
	}
}

func TestTaskSummaryDeliveryReportsRejection(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"http status": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"feishu code": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":19001,"msg":"param invalid"}`))
		},
		"wecom errcode": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook url"}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			if err := deliverTaskSummary(context.Background(), srv.Client(), srv.URL, "hi"); err == nil {
				t.Fatal("rejection reported as delivered")
			}
		})
	}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"code":0,"errcode":0}`)) }))
	defer ok.Close()
	if err := deliverTaskSummary(context.Background(), ok.Client(), ok.URL, "hi"); err != nil {
		t.Fatalf("accepted message reported as failed: %v", err)
	}
}

// The production client is the watch client, which must not reach the
// deployment's own network even when the user points the webhook there.
func TestTaskSummaryProductionClientRefusesLoopback(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	store := &fakeTaskNotifyStore{task: notifyTask(srv.URL)}
	n := &TaskNotifier{Store: store, Client: scheduled_task.WatchClient()}
	n.RunFinished(context.Background(), TaskRunResult{TaskID: "task-1", OwnerEmail: "owner@example.test", Status: db.RunDone, Answer: "x"})
	if hit {
		t.Fatal("outbound notification reached a loopback address")
	}
	if err := deliverTaskSummary(context.Background(), scheduled_task.WatchClient(), srv.URL, "x"); err == nil {
		t.Fatal("loopback delivery reported as sent")
	}
	if len(store.saved) != 1 {
		t.Fatal("blocked delivery must not drop the in-app notification")
	}
}

func TestValidateNotifyWebhook(t *testing.T) {
	for _, bad := range []string{"ftp://example.com/x", "example.com/hook", "https://", "https://user:pw@example.com/x"} {
		if _, err := ValidateNotifyWebhook(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for in, want := range map[string]string{"": "", "  https://open.feishu.cn/open-apis/bot/v2/hook/x ": "https://open.feishu.cn/open-apis/bot/v2/hook/x"} {
		if got, err := ValidateNotifyWebhook(in); err != nil || got != want {
			t.Fatalf("%q → %q %v", in, got, err)
		}
	}
}
