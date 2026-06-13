package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ListNotifications returns the user's notifications + unread count.
func (a *API) ListNotifications(ctx context.Context, c *app.RequestContext) {
	var req struct {
		UnreadOnly bool `json:"unread_only"`
	}
	_ = bind(c, &req)
	email := authEmail(c)
	items, err := a.Store.ListNotifications(ctx, email, req.UnreadOnly)
	if err != nil {
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	unread, _ := a.Store.UnreadCount(ctx, email)
	ok(c, map[string]any{"notifications": items, "unread": unread})
}

// ReadNotifications marks one (notification_id) or all of the user's read.
func (a *API) ReadNotifications(ctx context.Context, c *app.RequestContext) {
	var req struct {
		NotificationID string `json:"notification_id"`
	}
	_ = bind(c, &req)
	if err := a.Store.MarkNotificationsRead(ctx, authEmail(c), req.NotificationID); err != nil {
		fail(c, consts.StatusInternalServerError, "mark failed")
		return
	}
	ok(c, map[string]string{"status": "ok"})
}
