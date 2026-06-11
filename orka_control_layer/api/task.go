package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
)

type createTaskReq struct {
	ConversationID    string         `json:"conversation_id"`
	InitialTemplateId string         `json:"initial_template_id"`
	OwnerEmail        string         `json:"owner_email"`
	Variables         map[string]any `json:"variables"`
}

type getTasksReq struct {
	ConversationID string `json:"conversation_id"`
	OwnerEmail     string `json:"owner_email"`
	Page           int64  `json:"page"`
	Size           int64  `json:"size"`
}

// CreateTask creates a task (optionally linked to a conversation).
func (a *API) CreateTask(ctx context.Context, c *app.RequestContext) {
	var req createTaskReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	owner := authEmail(c)
	if owner == "" {
		owner = req.OwnerEmail
	}
	t := &db.TaskMeta{
		TaskID:            messages.NewID(),
		InitialTemplateId: req.InitialTemplateId,
		ConversationID:    req.ConversationID,
		OwnerEmail:        owner,
		Variables:         req.Variables,
		RunStatus:         db.RunStart,
		CronStatus:        "off",
		CreatedAt:         time.Now().UnixMilli(),
	}
	if err := a.Store.CreateTask(ctx, t); err != nil {
		a.Log.Error("create task", "err", err)
		fail(c, consts.StatusInternalServerError, "create failed")
		return
	}
	if req.ConversationID != "" {
		if err := a.Store.AddTaskToConversation(ctx, req.ConversationID, t.TaskID); err != nil {
			a.Log.Warn("link task to conversation", "err", err)
		}
	}
	ok(c, t)
}

type scheduleReq struct {
	ConversationID string `json:"conversation_id"`
	Prompt         string `json:"prompt"`
	IntervalSec    int64  `json:"interval_sec"`
	Title          string `json:"title"`
}

// ScheduleTask creates a recurring task: every IntervalSec the scheduler renders
// Prompt and triggers a chat run for the owner.
func (a *API) ScheduleTask(ctx context.Context, c *app.RequestContext) {
	var req scheduleReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	owner := authEmail(c)
	if owner == "" {
		fail(c, consts.StatusUnauthorized, "login required")
		return
	}
	if req.Prompt == "" || req.IntervalSec < 30 {
		fail(c, consts.StatusBadRequest, "prompt required and interval_sec >= 30")
		return
	}
	now := time.Now().UnixMilli()
	t := &db.TaskMeta{
		TaskID:         messages.NewID(),
		ConversationID: req.ConversationID,
		OwnerEmail:     owner,
		Variables:      map[string]any{"prompt_template": req.Prompt, "title": req.Title},
		RunStatus:      db.RunStart,
		CronStatus:     "on",
		IntervalSec:    req.IntervalSec,
		NextRunAt:      now + req.IntervalSec*1000, // first run one interval from now
		CreatedAt:      now,
	}
	if err := a.Store.CreateTask(ctx, t); err != nil {
		a.Log.Error("schedule task", "err", err)
		fail(c, consts.StatusInternalServerError, "schedule failed")
		return
	}
	if req.ConversationID != "" {
		_ = a.Store.AddTaskToConversation(ctx, req.ConversationID, t.TaskID)
	}
	ok(c, t)
}

type taskIDReq struct {
	TaskID string `json:"task_id"`
}

// UnscheduleTask turns a recurring task off (owner-checked).
func (a *API) UnscheduleTask(ctx context.Context, c *app.RequestContext) {
	var req taskIDReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	t, err := a.Store.GetTask(ctx, req.TaskID)
	if err != nil || t.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "task not found")
		return
	}
	if err := a.Store.SetTaskCron(ctx, req.TaskID, "off", t.IntervalSec, 0); err != nil {
		fail(c, consts.StatusInternalServerError, "update failed")
		return
	}
	ok(c, map[string]string{"status": "off", "task_id": req.TaskID})
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
