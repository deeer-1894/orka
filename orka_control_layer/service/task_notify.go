package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

// TaskNotifier tells a task's owner that a triggered run finished. A task runs
// in its own conversation, which nobody is looking at when a schedule, a page
// change or a webhook fires it; without this the result simply waits there.
// Every finished run lands in the in-app bell; a task with NotifyWebhook also
// pushes a short summary to that group-bot address.
type TaskNotifier struct {
	Store taskNotifyStore
	// Client sends outbound webhooks. The address is user input and the
	// server sits inside the deployment's network, so it must refuse private
	// and loopback targets: main wires scheduled_task.WatchClient. nil turns
	// outbound delivery off.
	Client *http.Client
	// PublicURL is the web app's origin, used for the conversation link in
	// outbound messages ("" = no link, the conversation ID instead).
	PublicURL string
	OnEvent   func(email, kind string)
	Log       *slog.Logger
	Now       func() time.Time
}

type taskNotifyStore interface {
	GetTask(ctx context.Context, id string) (*db.TaskMeta, error)
	CreateNotification(ctx context.Context, n *db.Notification) error
}

// TaskRunResult is what a finished task run reports to its owner.
type TaskRunResult struct {
	TaskID, RunID, ConversationID, OwnerEmail string
	Status                                    string // done | partial | failed | paused | interrupted
	Answer                                    string // the final answer
	Error                                     string
}

const (
	notifyExcerptLines = 3
	notifyExcerptRunes = 240
	notifyBodyLimit    = 64 << 10
)

// RunFinished records the in-app notification and, when the task asks for it,
// delivers the outbound summary. Delivery failures are logged, never retried:
// the in-app notification already carries the result.
func (n *TaskNotifier) RunFinished(ctx context.Context, r TaskRunResult) {
	if n == nil || n.Store == nil || r.TaskID == "" {
		return
	}
	task, err := n.Store.GetTask(ctx, r.TaskID)
	if err != nil || task.OwnerEmail == "" || (r.OwnerEmail != "" && task.OwnerEmail != r.OwnerEmail) {
		return
	}
	if r.ConversationID == "" {
		r.ConversationID = task.ConversationID
	}
	title := taskTitle(task)
	label := taskStatusLabel(r.Status)
	detail := runExcerpt(r)
	err = n.Store.CreateNotification(ctx, &db.Notification{
		NotificationID: "ntf_" + messages.NewID(),
		OwnerEmail:     task.OwnerEmail,
		Kind:           "task_run",
		Title:          fmt.Sprintf("任务%s：%s", label, title),
		Body:           detail,
		RunID:          r.RunID,
		ConversationID: r.ConversationID,
		CreatedAt:      n.now().UnixMilli(),
	})
	if err != nil && n.Log != nil {
		n.Log.Warn("task notification", "task_id", r.TaskID, "err", err)
	}
	if err == nil && n.OnEvent != nil {
		n.OnEvent(task.OwnerEmail, "notification")
	}
	if task.NotifyWebhook == "" || n.Client == nil {
		return
	}
	text := n.summary(title, label, detail, r.ConversationID)
	if err := deliverTaskSummary(ctx, n.Client, task.NotifyWebhook, text); err != nil && n.Log != nil {
		n.Log.Warn("task outbound notification", "task_id", r.TaskID, "err", err)
	}
}

func (n *TaskNotifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *TaskNotifier) summary(title, label, detail, conversationID string) string {
	lines := []string{fmt.Sprintf("【Orka 任务%s】%s", label, title)}
	if detail != "" {
		lines = append(lines, detail)
	}
	if base := strings.TrimRight(n.PublicURL, "/"); base != "" && conversationID != "" {
		lines = append(lines, "查看对话："+base+"/?c="+url.QueryEscape(conversationID))
	} else if conversationID != "" {
		lines = append(lines, "对话 ID："+conversationID)
	}
	return strings.Join(lines, "\n")
}

func taskTitle(t *db.TaskMeta) string {
	for _, k := range []string{"title", "prompt_template", "prompt"} {
		if s, ok := t.Variables[k].(string); ok && strings.TrimSpace(s) != "" {
			return trunc(strings.TrimSpace(s), 80)
		}
	}
	return "未命名任务"
}

func taskStatusLabel(status string) string {
	switch status {
	case db.RunDone:
		return "已完成"
	case db.RunPartial:
		return "部分完成"
	case db.RunFailed:
		return "失败"
	case db.RunPaused:
		return "等待确认"
	case db.RunInterrupted:
		return "已中断"
	}
	return "已结束"
}

// runExcerpt is the first few non-empty lines of the answer, or the error when
// the run produced none.
func runExcerpt(r TaskRunResult) string {
	text := r.Answer
	if strings.TrimSpace(text) == "" || r.Status == db.RunFailed && r.Error != "" {
		text = r.Error
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
			if len(kept) == notifyExcerptLines {
				break
			}
		}
	}
	out := strings.Join(kept, "\n")
	if r := []rune(out); len(r) > notifyExcerptRunes {
		out = string(r[:notifyExcerptRunes]) + "…"
	}
	return out
}

// taskSummaryPayload shapes the text for the receiving bot. Feishu/Lark and
// WeCom/DingTalk each expect their own envelope; anything else gets the
// Slack incoming-webhook shape, which most chat tools also accept.
func taskSummaryPayload(webhook, text string) ([]byte, error) {
	u, err := url.Parse(webhook)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "open.feishu.cn" || host == "open.larksuite.com":
		return json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
	case host == "qyapi.weixin.qq.com" || host == "oapi.dingtalk.com":
		return json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": text}})
	}
	return json.Marshal(map[string]string{"text": text})
}

// deliverTaskSummary posts the summary. Feishu, WeCom and DingTalk answer
// HTTP 200 with a non-zero code when they reject a message, so the body is
// checked too.
func deliverTaskSummary(ctx context.Context, client *http.Client, webhook, text string) error {
	body, err := taskSummaryPayload(webhook, text)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Orka-Notify/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, notifyBodyLimit))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	var reply struct {
		Code    *int   `json:"code"`
		ErrCode *int   `json:"errcode"`
		Msg     string `json:"msg"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(raw, &reply) == nil {
		if reply.Code != nil && *reply.Code != 0 {
			return fmt.Errorf("webhook rejected the message: %d %s", *reply.Code, reply.Msg)
		}
		if reply.ErrCode != nil && *reply.ErrCode != 0 {
			return fmt.Errorf("webhook rejected the message: %d %s", *reply.ErrCode, reply.ErrMsg)
		}
	}
	return nil
}

// ValidateNotifyWebhook checks the shape of an outbound address at save time.
// Where it may point is decided at send time by the client, which resolves
// the host on every dial.
func ValidateNotifyWebhook(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(raw) > 2048 {
		return "", fmt.Errorf("通知地址需是完整的 http(s) 网址")
	}
	return raw, nil
}
