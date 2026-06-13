package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/service"
)

// redactHeaders returns header KEYS only — values are secrets (API keys) and
// must never be sent back to the client.
func redactHeaders(h map[string]string) map[string]string {
	out := map[string]string{}
	for k := range h {
		out[k] = "••••"
	}
	return out
}

// ListConnectors returns the user's external MCP connectors (header values redacted).
func (a *API) ListConnectors(ctx context.Context, c *app.RequestContext) {
	conns, err := a.Store.ListConnectors(ctx, authEmail(c))
	if err != nil {
		a.Log.Error("list connectors", "err", err)
		fail(c, consts.StatusInternalServerError, "list failed")
		return
	}
	for i := range conns {
		conns[i].Headers = redactHeaders(conns[i].Headers)
	}
	ok(c, map[string]any{"connectors": conns})
}

type connectorReq struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	URL       string            `json:"url"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Headers   map[string]string `json:"headers"`
}

func (r connectorReq) toConnector(owner string) db.Connector {
	return db.Connector{
		ConnectorID: "conn_" + messages.NewID(),
		OwnerEmail:  owner,
		Name:        r.Name,
		Transport:   r.Transport,
		URL:         r.URL,
		Command:     r.Command,
		Args:        r.Args,
		Headers:     r.Headers,
		Enabled:     true,
		CreatedAt:   time.Now().UnixMilli(),
	}
}

// TestConnector probes a server (connects + lists tools) WITHOUT saving — so the
// UI can validate before committing.
func (a *API) TestConnector(ctx context.Context, c *app.RequestContext) {
	var req connectorReq
	if err := bind(c, &req); err != nil {
		fail(c, consts.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	names, err := service.ProbeConnector(ctx, req.toConnector(authEmail(c)))
	if err != nil {
		ok(c, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ok(c, map[string]any{"ok": true, "tools": names})
}

// CreateConnector saves a connector and busts the user's tool cache so its tools
// appear on the next run.
func (a *API) CreateConnector(ctx context.Context, c *app.RequestContext) {
	var req connectorReq
	if err := bind(c, &req); err != nil || req.Name == "" {
		fail(c, consts.StatusBadRequest, "name required")
		return
	}
	conn := req.toConnector(authEmail(c))
	if err := a.Store.CreateConnector(ctx, &conn); err != nil {
		a.Log.Error("create connector", "err", err)
		fail(c, consts.StatusInternalServerError, "create failed")
		return
	}
	if a.Chat != nil && a.Chat.InvalidateTools != nil {
		a.Chat.InvalidateTools(authEmail(c))
	}
	conn.Headers = redactHeaders(conn.Headers)
	ok(c, conn)
}

type connectorIDReq struct {
	ConnectorID string `json:"connector_id"`
}

// DeleteConnector removes a connector and busts the user's tool cache.
func (a *API) DeleteConnector(ctx context.Context, c *app.RequestContext) {
	var req connectorIDReq
	if err := bind(c, &req); err != nil || req.ConnectorID == "" {
		fail(c, consts.StatusBadRequest, "connector_id required")
		return
	}
	if err := a.Store.DeleteConnector(ctx, req.ConnectorID, authEmail(c)); err != nil {
		fail(c, consts.StatusInternalServerError, "delete failed")
		return
	}
	if a.Chat != nil && a.Chat.InvalidateTools != nil {
		a.Chat.InvalidateTools(authEmail(c))
	}
	ok(c, map[string]string{"status": "deleted"})
}
