package api

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/connectoroauth"
)

const oauthCallbackPath = "/api/v1/controller/connector/oauth/callback"

// ListOAuthProviders lists the one-click connectors and whether this
// deployment has what each needs.
func (a *API) ListOAuthProviders(_ context.Context, c *app.RequestContext) {
	ok(c, connectoroauth.Providers())
}

// StartConnectorOAuth returns the URL the browser opens to authorize.
func (a *API) StartConnectorOAuth(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Provider    string `json:"provider"`
		MCPURL      string `json:"mcp_url"`
		ConnectorID string `json:"connector_id"`
	}
	if err := bind(c, &req); err != nil || req.Provider == "" {
		fail(c, consts.StatusBadRequest, "provider required")
		return
	}
	if a.ConnectorOAuth == nil {
		fail(c, consts.StatusServiceUnavailable, "一键授权未启用")
		return
	}
	redirect, err := oauthRedirect(c)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	link, err := a.ConnectorOAuth.Start(ctx, connectoroauth.StartRequest{Owner: authEmail(c), Provider: req.Provider, MCPURL: req.MCPURL, ConnectorID: req.ConnectorID, Redirect: redirect})
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	ok(c, map[string]string{"auth_url": link})
}

// ConnectorOAuthCallback is where the provider sends the browser back. It is
// public (a redirect carries no session); the single-use state binds it to
// the user who started. It answers with a page that tells the opener window
// and closes itself.
func (a *API) ConnectorOAuthCallback(ctx context.Context, c *app.RequestContext) {
	state, code, denied := string(c.Query("state")), string(c.Query("code")), string(c.Query("error"))
	result := map[string]any{"type": "orka-connector-oauth", "ok": false}
	switch {
	case a.ConnectorOAuth == nil:
		result["error"] = "一键授权未启用"
	case denied != "":
		result["error"] = "你取消了授权"
		_, _ = a.ConnectorOAuth.Complete(ctx, state, "") // consume the state
	default:
		cn, err := a.ConnectorOAuth.Complete(ctx, state, code)
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["ok"], result["connector_id"], result["name"] = true, cn.ConnectorID, cn.Name
			if a.Chat != nil && a.Chat.InvalidateTools != nil {
				a.Chat.InvalidateTools(cn.OwnerEmail)
			}
		}
	}
	payload, _ := json.Marshal(result)
	title := "连接成功"
	message := "已连接，可以关闭这个窗口。"
	if result["ok"] != true {
		title, message = "连接失败", result["error"].(string)
	}
	c.Response.Header.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
	c.Response.Header.Set("Referrer-Policy", "no-referrer")
	c.Data(consts.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+title+`</title>
<body style="font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;color:#1d1c1a;background:#f7f6f3"><div style="text-align:center"><h1 style="font-size:18px">`+title+`</h1><p style="color:#5e5a54">`+html.EscapeString(message)+`</p></div>
<script>try{if(window.opener){window.opener.postMessage(`+string(payload)+`,location.origin);setTimeout(function(){window.close()},800)}}catch(e){}</script></body>`))
}

// oauthRedirect is the callback as the user's browser reaches this server:
// ORKA_PUBLIC_URL when set, otherwise the origin the request came from. It
// only has to be reachable by that browser, not from the internet.
func oauthRedirect(c *app.RequestContext) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("ORKA_PUBLIC_URL")), "/")
	if base == "" {
		base = string(c.GetHeader("Origin"))
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errSentence("无法确定回调地址，请设置 ORKA_PUBLIC_URL")
	}
	return u.Scheme + "://" + u.Host + oauthCallbackPath, nil
}

type errSentence string

func (e errSentence) Error() string { return string(e) }
