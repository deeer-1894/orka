package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/db"
)

type createConvReq struct {
	Title string `json:"title"`
}

type getConvReq struct {
	ConversationID string `json:"conversation_id"`
}

type getMessagesReq struct {
	ConversationID string `json:"conversation_id"`
	Page           int64  `json:"page"`
	Size           int64  `json:"size"`
}

// CreateConversation creates a new conversation shell.
func (a *API) CreateConversation(ctx context.Context, c *app.RequestContext) {
	var req createConvReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	title := req.Title
	if title == "" {
		title = "New Conversation"
	}
	conv := &db.ConversationTable{
		ConversationID: messages.NewID(),
		OwnerEmail:     authEmail(c),
		Title:          title,
		TaskIds:        []string{},
		CreatedAt:      time.Now().UnixMilli(),
	}
	if err := a.Store.CreateConversation(ctx, conv); err != nil {
		a.Log.Error("create conversation", "err", err)
		fail(c, consts.StatusInternalServerError, "create failed")
		return
	}
	ok(c, conv)
}

// GetConversation fetches one conversation by id.
func (a *API) GetConversation(ctx context.Context, c *app.RequestContext) {
	var req getConvReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required")
		return
	}
	conv, err := a.Store.GetConversation(ctx, req.ConversationID)
	if err == db.ErrNotFound {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.Log.Error("get conversation", "err", err)
		fail(c, consts.StatusInternalServerError, "get failed")
		return
	}
	if conv.OwnerEmail != "" && conv.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "not found") // don't leak existence
		return
	}
	ok(c, conv)
}

type renameReq struct {
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title"`
}

// RenameConversation updates a conversation's title (owner only).
func (a *API) RenameConversation(ctx context.Context, c *app.RequestContext) {
	var req renameReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" || req.Title == "" {
		fail(c, consts.StatusBadRequest, "conversation_id and title required")
		return
	}
	conv, err := a.Store.GetConversation(ctx, req.ConversationID)
	if err != nil || (conv.OwnerEmail != "" && conv.OwnerEmail != authEmail(c)) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	if err := a.Store.UpdateConversationTitle(ctx, req.ConversationID, req.Title); err != nil {
		fail(c, consts.StatusInternalServerError, "rename failed")
		return
	}
	ok(c, map[string]string{"conversation_id": req.ConversationID, "title": req.Title})
}

// DeleteConversation removes a conversation and its data (owner only).
func (a *API) DeleteConversation(ctx context.Context, c *app.RequestContext) {
	var req getConvReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required")
		return
	}
	conv, err := a.Store.GetConversation(ctx, req.ConversationID)
	if err != nil || (conv.OwnerEmail != "" && conv.OwnerEmail != authEmail(c)) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	if err := a.Store.DeleteConversation(ctx, req.ConversationID); err != nil {
		fail(c, consts.StatusInternalServerError, "delete failed")
		return
	}
	ok(c, map[string]string{"deleted": req.ConversationID})
}

// ListConversations returns the authenticated user's conversations.
func (a *API) ListConversations(ctx context.Context, c *app.RequestContext) {
	convs, err := a.Store.ListConversationsByOwner(ctx, authEmail(c), 0, 100)
	if err != nil {
		a.Log.Error("list conversations", "err", err)
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, convs)
}

// GetMessages lists messages of a conversation (paginated).
func (a *API) GetMessages(ctx context.Context, c *app.RequestContext) {
	var req getMessagesReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required")
		return
	}
	// ownership check: only the conversation's owner may read its messages
	if conv, err := a.Store.GetConversation(ctx, req.ConversationID); err == nil &&
		conv.OwnerEmail != "" && conv.OwnerEmail != authEmail(c) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	msgs, err := a.Store.GetMessages(ctx, req.ConversationID, req.Page, req.Size)
	if err != nil {
		a.Log.Error("get messages", "err", err)
		fail(c, consts.StatusInternalServerError, "get failed")
		return
	}
	ok(c, msgs)
}
