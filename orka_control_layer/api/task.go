package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
)

type getTasksReq struct {
	ConversationID string `json:"conversation_id"`
	OwnerEmail     string `json:"owner_email"`
	Page           int64  `json:"page"`
	Size           int64  `json:"size"`
}

type saveTaskReq struct {
	TaskID     string         `json:"task_id"`
	Title      string         `json:"title"`
	Prompt     string         `json:"prompt"`
	Trigger    db.TaskTrigger `json:"trigger"`
	Enabled    bool           `json:"enabled"`
	RetryCount int            `json:"retry_count"`
	// NotifyWebhook is an optional group-bot address for run summaries.
	NotifyWebhook string `json:"notify_webhook"`
	// ToolPolicy is what unattended runs may do; omitted keeps the task's
	// current policy, or the default for a new task.
	ToolPolicy *db.TaskToolPolicy `json:"tool_policy"`
}

type taskIDReq struct {
	TaskID  string `json:"task_id"`
	Enabled bool   `json:"enabled"`
}

// SaveTask creates or edits a triggered task. A new task gets its own
// conversation, where every run's transcript and files accumulate.
func (a *API) SaveTask(ctx context.Context, c *app.RequestContext) {
	var req saveTaskReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	owner := authEmail(c)
	if owner == "" {
		fail(c, consts.StatusUnauthorized, "login required")
		return
	}
	req.Prompt, req.Title = strings.TrimSpace(req.Prompt), strings.TrimSpace(req.Title)
	if req.Prompt == "" || utf8.RuneCountInString(req.Prompt) > 8000 {
		fail(c, consts.StatusBadRequest, "任务内容不能为空，且不超过 8000 字")
		return
	}
	if req.Title == "" {
		req.Title = truncRunes(req.Prompt, 24)
	}
	if utf8.RuneCountInString(req.Title) > 80 || req.RetryCount < 0 || req.RetryCount > 3 {
		fail(c, consts.StatusBadRequest, "名称不超过 80 字，失败重试 0–3 次")
		return
	}
	trigger, err := req.Trigger.Normalize()
	if err != nil {
		fail(c, consts.StatusBadRequest, "触发条件无效："+err.Error())
		return
	}
	notify, err := service.ValidateNotifyWebhook(req.NotifyWebhook)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	var policy *db.TaskToolPolicy
	if req.ToolPolicy != nil {
		p, err := req.ToolPolicy.Normalize()
		if err != nil {
			fail(c, consts.StatusBadRequest, err.Error())
			return
		}
		policy = &p
	}
	now := time.Now().UnixMilli()
	task := &db.TaskMeta{}
	if req.TaskID != "" {
		existing, err := a.Store.GetTask(ctx, req.TaskID)
		if err != nil || existing.OwnerEmail != owner {
			fail(c, consts.StatusNotFound, "task not found")
			return
		}
		task = existing
		if old := existing.Trigger; old != nil && old.Kind == db.TriggerCondition && old.WatchURL == trigger.WatchURL && old.Contains == trigger.Contains {
			trigger.Fingerprint = old.Fingerprint
		}
	}
	task.Variables = map[string]any{"prompt_template": req.Prompt, "title": req.Title}
	task.Trigger, task.IntervalSec, task.RetryCount = &trigger, 0, req.RetryCount
	task.NotifyWebhook = notify
	if policy != nil {
		task.ToolPolicy = policy
	} else if task.ToolPolicy == nil {
		d := db.DefaultTaskToolPolicy()
		task.ToolPolicy = &d
	}
	if trigger.Kind == db.TriggerEvent {
		if task.WebhookToken == "" {
			task.WebhookToken = newWebhookToken()
		}
	} else {
		task.WebhookToken = ""
	}
	if err := enableTask(task, req.Enabled, now); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if req.TaskID != "" {
		if err := a.Store.SaveTaskDefinition(ctx, task); err != nil {
			a.Log.Error("save task", "err", err)
			fail(c, consts.StatusInternalServerError, "save failed")
			return
		}
		ok(c, task)
		return
	}
	conv := &db.ConversationTable{ConversationID: messages.NewID(), OwnerEmail: owner, Title: "任务 · " + req.Title, TaskIds: []string{}, CreatedAt: now}
	if _, err := pathsafe.EnsureSession(a.BaseStorage, owner, conv.ConversationID); err != nil {
		workspaceFail(c, err)
		return
	}
	if err := a.Store.CreateConversation(ctx, conv); err != nil {
		a.Log.Error("create task conversation", "err", err)
		fail(c, consts.StatusInternalServerError, "save failed")
		return
	}
	task.TaskID, task.ConversationID, task.OwnerEmail, task.RunStatus, task.CreatedAt = messages.NewID(), conv.ConversationID, owner, db.RunStart, now
	if err := a.Store.CreateTask(ctx, task); err != nil {
		a.Log.Error("create task", "err", err)
		fail(c, consts.StatusInternalServerError, "save failed")
		return
	}
	if err := a.Store.AddTaskToConversation(ctx, conv.ConversationID, task.TaskID); err != nil {
		a.Log.Warn("link task to conversation", "err", err)
	}
	ok(c, task)
}

// enableTask sets the on/off state and the next due time. Re-enabling clears
// an automatic circuit-breaker shutdown.
func enableTask(task *db.TaskMeta, enabled bool, now int64) error {
	task.CronStatus, task.NextRunAt = "off", 0
	if !enabled {
		return nil
	}
	task.CronStatus, task.DisabledReason, task.ConsecutiveFails = "on", "", 0
	if task.Trigger != nil && task.Trigger.Kind == db.TriggerEvent {
		return nil
	}
	if task.Trigger != nil && task.Trigger.Kind == db.TriggerCondition {
		// Look now: the first check only records a baseline, so waiting a full
		// interval for it would delay the first possible trigger by two.
		task.NextRunAt = now
		return nil
	}
	task.NextRunAt = db.NextDue(*task, now)
	if task.NextRunAt == 0 {
		return errPastSchedule
	}
	return nil
}

type taskError string

func (e taskError) Error() string { return string(e) }

const errPastSchedule = taskError("触发时间已过，请选择未来的时间")

func newWebhookToken() string {
	var b [18]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (a *API) ownedTask(ctx context.Context, c *app.RequestContext) (*db.TaskMeta, *taskIDReq, bool) {
	var req taskIDReq
	if err := bind(c, &req); err != nil || req.TaskID == "" {
		fail(c, consts.StatusBadRequest, "task_id required")
		return nil, nil, false
	}
	t, err := a.Store.GetTask(ctx, req.TaskID)
	if err != nil || t.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "task not found")
		return nil, nil, false
	}
	return t, &req, true
}

// ToggleTask turns a task's trigger on or off.
func (a *API) ToggleTask(ctx context.Context, c *app.RequestContext) {
	t, req, found := a.ownedTask(ctx, c)
	if !found {
		return
	}
	if err := enableTask(t, req.Enabled, time.Now().UnixMilli()); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if err := a.Store.SaveTaskDefinition(ctx, t); err != nil {
		fail(c, consts.StatusInternalServerError, "update failed")
		return
	}
	ok(c, t)
}

// RunTaskNow starts one run immediately, whatever the trigger.
func (a *API) RunTaskNow(ctx context.Context, c *app.RequestContext) {
	t, _, found := a.ownedTask(ctx, c)
	if !found {
		return
	}
	go a.Chat.RunHeadless(context.Background(), service.ChatRunRequest{
		Message: taskPromptFor(t.Variables), ConversationID: t.ConversationID, TaskID: t.TaskID, UserEmail: t.OwnerEmail, Trigger: "manual",
	})
	c.JSON(consts.StatusAccepted, map[string]any{"code": 0, "data": map[string]string{"status": "started", "task_id": t.TaskID, "conversation_id": t.ConversationID}})
}

// DeleteTask removes a task; its conversation and run history remain.
func (a *API) DeleteTask(ctx context.Context, c *app.RequestContext) {
	t, _, found := a.ownedTask(ctx, c)
	if !found {
		return
	}
	if err := a.Store.DeleteTask(ctx, t.OwnerEmail, t.TaskID); err != nil {
		fail(c, consts.StatusInternalServerError, "delete failed")
		return
	}
	ok(c, map[string]string{"task_id": t.TaskID})
}

// GetTasks lists tasks filtered by conversation and/or owner.
func (a *API) GetTasks(ctx context.Context, c *app.RequestContext) {
	var req getTasksReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	// always scope to the authenticated user
	filter := bson.M{"owner_email": authEmail(c)}
	if req.ConversationID != "" {
		filter["conversation_id"] = req.ConversationID
	}
	tasks, err := a.Store.ListTasks(ctx, filter, req.Page, req.Size)
	if err != nil {
		a.Log.Error("list tasks", "err", err)
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}

	// Enrich owners via the cached directory (cache-first + batch miss),
	// avoiding an N+1 fan-out to the contact service.
	owners := map[string]any{}
	if a.Directory != nil {
		emails := make([]string, 0, len(tasks))
		for _, t := range tasks {
			if t.OwnerEmail != "" {
				emails = append(emails, t.OwnerEmail)
			}
		}
		if infos, err := a.Directory.Lookup(ctx, emails); err == nil {
			for e, info := range infos {
				owners[e] = info
			}
		}
	}
	ok(c, map[string]any{"tasks": tasks, "owners": owners})
}
