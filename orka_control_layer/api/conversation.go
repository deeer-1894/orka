package api

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
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

type searchReq struct {
	Query string `json:"query"`
	Limit int64  `json:"limit"`
}

// SearchConversations runs cross-conversation full-text search over the user's
// readable messages (owned + shared), returning matches with a snippet and the
// owning conversation title.
func (a *API) SearchConversations(ctx context.Context, c *app.RequestContext) {
	var req searchReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request")
		return
	}
	q := strings.TrimSpace(req.Query)
	if len(q) < 2 {
		ok(c, map[string]any{"hits": []db.MessageSearchHit{}})
		return
	}
	hits, err := a.Store.SearchMessages(ctx, authEmail(c), q, req.Limit)
	if err != nil {
		a.Log.Error("search messages", "err", err)
		fail(c, consts.StatusInternalServerError, "search failed")
		return
	}
	ok(c, map[string]any{"hits": hits})
}

type forkReq struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

// ForkConversation branches a conversation at a given message: a new copy the
// user owns, seeded with history up to that turn, nested under its parent.
func (a *API) ForkConversation(ctx context.Context, c *app.RequestContext) {
	var req forkReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" {
		fail(c, consts.StatusBadRequest, "conversation_id required")
		return
	}
	email := authEmail(c)
	if conv, err := a.Store.GetConversation(ctx, req.ConversationID); err != nil || !conv.CanRead(email) {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	branch, err := a.Store.ForkConversation(ctx, req.ConversationID, req.MessageID, email)
	if err != nil {
		a.Log.Error("fork conversation", "err", err)
		fail(c, consts.StatusInternalServerError, "fork failed")
		return
	}
	ok(c, branch)
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
	if !conv.CanRead(authEmail(c)) {
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

// PruneConversations deletes the user's empty (message-less) conversations.
func (a *API) PruneConversations(ctx context.Context, c *app.RequestContext) {
	owner := authEmail(c)
	if owner == "" {
		fail(c, consts.StatusUnauthorized, "login required")
		return
	}
	n, err := a.Store.PruneEmptyConversations(ctx, owner)
	if err != nil {
		a.Log.Error("prune conversations", "err", err)
		fail(c, consts.StatusInternalServerError, "prune failed")
		return
	}
	ok(c, map[string]int{"removed": n})
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
	// access check: owner or a shared user (viewer/editor) may read messages
	if conv, err := a.Store.GetConversation(ctx, req.ConversationID); err == nil && !conv.CanRead(authEmail(c)) {
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

type shareReq struct {
	ConversationID string `json:"conversation_id"`
	Email          string `json:"email"`
	Role           string `json:"role"` // viewer | editor | none (none removes)
}

// ShareConversation adds, updates, or removes a user's access (owner only).
func (a *API) ShareConversation(ctx context.Context, c *app.RequestContext) {
	var req shareReq
	if err := bind(c, &req); err != nil || req.ConversationID == "" || req.Email == "" {
		fail(c, consts.StatusBadRequest, "conversation_id and email required")
		return
	}
	me := authEmail(c)
	conv, err := a.Store.GetConversation(ctx, req.ConversationID)
	if err != nil || conv.OwnerEmail != me { // only the owner manages sharing
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	if req.Email == conv.OwnerEmail {
		fail(c, consts.StatusBadRequest, "owner already has full access")
		return
	}
	// Rebuild the share list: drop any existing entry for this email, then add
	// the new role unless it's "none" (removal).
	next := make([]db.ConversationShare, 0, len(conv.Shares)+1)
	for _, s := range conv.Shares {
		if s.Email != req.Email {
			next = append(next, s)
		}
	}
	switch req.Role {
	case db.RoleViewer, db.RoleEditor:
		next = append(next, db.ConversationShare{Email: req.Email, Role: req.Role})
	case "", "none", "remove":
		// removal — leave it out
	default:
		fail(c, consts.StatusBadRequest, "role must be viewer, editor, or none")
		return
	}
	if err := a.Store.UpdateConversationShares(ctx, req.ConversationID, next); err != nil {
		a.Log.Error("share conversation", "err", err)
		fail(c, consts.StatusInternalServerError, "share failed")
		return
	}
	ok(c, map[string]any{"conversation_id": req.ConversationID, "shares": next})
}

// SharedWithMe lists conversations other users have shared with the caller.
func (a *API) SharedWithMe(ctx context.Context, c *app.RequestContext) {
	convs, err := a.Store.ListSharedWith(ctx, authEmail(c), 0, 100)
	if err != nil {
		a.Log.Error("shared with me", "err", err)
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	ok(c, convs)
}
