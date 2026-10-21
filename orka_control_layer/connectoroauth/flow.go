package connectoroauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/messages"
)

// Store is what the flow needs from persistence.
type Store interface {
	CreateConnector(ctx context.Context, c *db.Connector) error
	SaveConnectorOAuth(ctx context.Context, id, owner string, grant *db.ConnectorOAuth) error
}

// Manager runs one-click authorization: Start hands the browser an
// authorization URL, Complete turns the callback into a stored, sealed grant,
// and Authorize supplies the transport a current access token, refreshing it
// shortly before it expires. Tokens never reach the model or the browser.
type Manager struct {
	Store  Store
	Sealer *Sealer
	// Client makes every outbound call. Addresses can be user input, so it
	// must refuse private and loopback targets (main wires the watch client).
	Client *http.Client
	Now    func() time.Time

	mu      sync.Mutex
	pending map[string]*pendingAuth
	grants  map[string]*db.ConnectorOAuth // latest grant per connector (refresh rotates tokens)
	refresh map[string]*sync.Mutex
}

type pendingAuth struct {
	owner, provider, name, mcpURL, connectorID string
	redirect, verifier                         string
	clientID, clientSecret, tokenURL, resource string
	scopes                                     string
	style                                      tokenStyle
	created                                    time.Time
}

const (
	pendingTTL     = 10 * time.Minute
	refreshLeeway  = 2 * time.Minute
	maxPending     = 1000
	tokenBodyLimit = 64 << 10
)

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// StartRequest is what the user picked.
type StartRequest struct {
	Owner       string
	Provider    string
	MCPURL      string // only for the generic MCP provider
	ConnectorID string // re-authorize this connector instead of creating one
	Redirect    string // the callback URL as the user's browser reaches it
}

// Start returns the URL to open for the user to authorize.
func (m *Manager) Start(ctx context.Context, r StartRequest) (string, error) {
	p, ok := ProviderByID(r.Provider)
	if !ok {
		return "", errors.New("未知的连接方式")
	}
	if !p.Ready {
		return "", errors.New(p.Missing)
	}
	mcpURL := p.MCPURL
	if p.ID == ProviderMCP {
		u, err := url.Parse(strings.TrimSpace(r.MCPURL))
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", errors.New("请填写 https 开头的 MCP 服务地址")
		}
		mcpURL = u.String()
	}
	pa := &pendingAuth{owner: r.Owner, provider: p.ID, name: p.Name, mcpURL: mcpURL, connectorID: r.ConnectorID, redirect: r.Redirect,
		verifier: randomToken(48), clientID: p.clientID, clientSecret: p.clientSecret, tokenURL: p.tokenURL, scopes: p.scopes, style: p.tokenStyle, created: m.now()}
	authorize := p.authorizeURL
	if p.discover {
		ep, err := discover(ctx, m.Client, mcpURL)
		if err != nil {
			return "", err
		}
		if ep.registration == "" {
			return "", errors.New("这个服务不支持自动注册客户端，请改用手动添加并填写令牌")
		}
		id, secret, err := register(ctx, m.Client, ep.registration, r.Redirect)
		if err != nil {
			return "", err
		}
		authorize, pa.tokenURL, pa.clientID, pa.clientSecret, pa.scopes, pa.resource = ep.authorize, ep.token, id, secret, ep.scopes, mcpURL
		if p.ID == ProviderMCP {
			if u, err := url.Parse(mcpURL); err == nil {
				pa.name = u.Host
			}
		}
	}
	state := randomToken(32)
	m.mu.Lock()
	if m.pending == nil {
		m.pending = map[string]*pendingAuth{}
	}
	for k, v := range m.pending {
		if m.now().Sub(v.created) > pendingTTL {
			delete(m.pending, k)
		}
	}
	if len(m.pending) >= maxPending {
		m.mu.Unlock()
		return "", errors.New("授权请求过多，请稍后再试")
	}
	m.pending[state] = pa
	m.mu.Unlock()

	sum := sha256.Sum256([]byte(pa.verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {pa.clientID}, "redirect_uri": {r.Redirect}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	if pa.scopes != "" {
		q.Set("scope", pa.scopes)
	}
	if pa.resource != "" {
		q.Set("resource", pa.resource)
	}
	sep := "?"
	if strings.Contains(authorize, "?") {
		sep = "&"
	}
	return authorize + sep + q.Encode(), nil
}

// Complete exchanges the callback's code and stores the grant. The state is
// single-use and bound to the user who started the flow.
func (m *Manager) Complete(ctx context.Context, state, code string) (*db.Connector, error) {
	m.mu.Lock()
	pa := m.pending[state]
	delete(m.pending, state)
	m.mu.Unlock()
	if pa == nil || m.now().Sub(pa.created) > pendingTTL {
		return nil, errors.New("授权已过期，请重新连接")
	}
	if code == "" {
		return nil, errors.New("授权未完成")
	}
	tok, err := m.exchange(ctx, pa.style, pa.tokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {pa.redirect},
		"client_id": {pa.clientID}, "code_verifier": {pa.verifier},
	}, pa.clientSecret, pa.resource)
	if err != nil {
		return nil, err
	}
	grant := &db.ConnectorOAuth{Provider: pa.provider, TokenURL: pa.tokenURL, ClientID: pa.clientID, Resource: pa.resource, Scopes: tok.Scope}
	if err := m.seal(grant, tok, pa.clientSecret); err != nil {
		return nil, err
	}
	grant.Account = m.account(ctx, pa.provider, tok.AccessToken)
	cn := &db.Connector{ConnectorID: pa.connectorID, OwnerEmail: pa.owner, Name: pa.name, Transport: "streamable_http", URL: pa.mcpURL, Enabled: true, CreatedAt: m.now().UnixMilli(), OAuth: grant}
	if pa.connectorID != "" {
		if err := m.Store.SaveConnectorOAuth(ctx, pa.connectorID, pa.owner, grant); err != nil {
			return nil, err
		}
	} else {
		cn.ConnectorID = "cn_" + messages.NewID()
		if err := m.Store.CreateConnector(ctx, cn); err != nil {
			return nil, err
		}
	}
	m.remember(cn.ConnectorID, grant)
	return cn, nil
}

// Authorize returns the headers a connector's MCP requests carry: its
// current access token, refreshed when close to expiry. A refresh that fails
// marks the grant so the UI can ask the user to authorize again.
func (m *Manager) Authorize(ctx context.Context, cn db.Connector) (map[string]string, error) {
	if cn.OAuth == nil {
		return nil, nil
	}
	lock := m.lockFor(cn.ConnectorID)
	lock.Lock()
	defer lock.Unlock()
	grant := m.latest(cn.ConnectorID, cn.OAuth)
	if grant.AuthError != "" {
		return nil, errors.New(grant.AuthError)
	}
	if grant.ExpiresAt > 0 && m.now().Add(refreshLeeway).UnixMilli() >= grant.ExpiresAt {
		next, err := m.refreshGrant(ctx, grant)
		if err != nil {
			failed := *grant
			failed.AuthError = "授权已失效，请重新授权"
			_ = m.Store.SaveConnectorOAuth(context.WithoutCancel(ctx), cn.ConnectorID, cn.OwnerEmail, &failed)
			m.remember(cn.ConnectorID, &failed)
			return nil, err
		}
		if err := m.Store.SaveConnectorOAuth(context.WithoutCancel(ctx), cn.ConnectorID, cn.OwnerEmail, next); err != nil {
			return nil, err
		}
		m.remember(cn.ConnectorID, next)
		grant = next
	}
	token, err := m.Sealer.Open(grant.AccessToken)
	if err != nil {
		return nil, err
	}
	return map[string]string{"Authorization": "Bearer " + token}, nil
}

func (m *Manager) refreshGrant(ctx context.Context, grant *db.ConnectorOAuth) (*db.ConnectorOAuth, error) {
	refresh, err := m.Sealer.Open(grant.RefreshToken)
	if err != nil || refresh == "" {
		return nil, errors.New("没有可用的刷新令牌")
	}
	secret, _ := m.Sealer.Open(grant.ClientSecret)
	style := formToken
	if grant.Provider == "feishu" {
		style = jsonToken
	}
	tok, err := m.exchange(ctx, style, grant.TokenURL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {grant.ClientID}}, secret, grant.Resource)
	if err != nil {
		return nil, err
	}
	next := *grant
	if tok.RefreshToken == "" {
		tok.RefreshToken = refresh // providers that do not rotate keep the old one
	}
	if tok.Scope != "" {
		next.Scopes = tok.Scope
	}
	return &next, m.seal(&next, tok, secret)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
	Code         *int   `json:"code"` // Feishu: non-zero on failure
	Msg          string `json:"msg"`
}

func (m *Manager) exchange(ctx context.Context, style tokenStyle, tokenURL string, form url.Values, secret, resource string) (tokenResponse, error) {
	if secret != "" {
		form.Set("client_secret", secret)
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	var body io.Reader
	contentType := "application/x-www-form-urlencoded"
	if style == jsonToken {
		obj := map[string]string{}
		for k := range form {
			obj[k] = form.Get(k)
		}
		raw, _ := json.Marshal(obj)
		body, contentType = bytes.NewReader(raw), "application/json; charset=utf-8"
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, body)
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := m.Client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("换取令牌失败：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, tokenBodyLimit))
	var tok tokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return tokenResponse{}, fmt.Errorf("换取令牌失败：HTTP %d", resp.StatusCode)
	}
	if tok.Error != "" || (tok.Code != nil && *tok.Code != 0) || tok.AccessToken == "" {
		reason := strings.TrimSpace(tok.ErrorDesc + " " + tok.Error + " " + tok.Msg)
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return tokenResponse{}, fmt.Errorf("换取令牌失败：%s", reason)
	}
	return tok, nil
}

func (m *Manager) seal(grant *db.ConnectorOAuth, tok tokenResponse, clientSecret string) error {
	var err error
	if grant.AccessToken, err = m.Sealer.Seal(tok.AccessToken); err != nil {
		return err
	}
	if grant.RefreshToken, err = m.Sealer.Seal(tok.RefreshToken); err != nil {
		return err
	}
	if grant.ClientSecret, err = m.Sealer.Seal(clientSecret); err != nil {
		return err
	}
	grant.ExpiresAt, grant.AuthError = 0, ""
	if tok.ExpiresIn > 0 {
		grant.ExpiresAt = m.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli()
	}
	return nil
}

// account names who authorized, for display; best effort.
func (m *Manager) account(ctx context.Context, provider, token string) string {
	var link, field string
	switch provider {
	case "github":
		link, field = "https://api.github.com/user", "login"
	case "feishu":
		link, field = "https://open.feishu.cn/open-apis/authen/v1/user_info", "name"
	default:
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.Client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil {
		return ""
	}
	if data, ok := out["data"].(map[string]any); ok {
		out = data
	}
	name, _ := out[field].(string)
	return name
}

func (m *Manager) lockFor(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refresh == nil {
		m.refresh = map[string]*sync.Mutex{}
	}
	if m.refresh[id] == nil {
		m.refresh[id] = &sync.Mutex{}
	}
	return m.refresh[id]
}

func (m *Manager) latest(id string, stored *db.ConnectorOAuth) *db.ConnectorOAuth {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g := m.grants[id]; g != nil {
		return g
	}
	return stored
}

func (m *Manager) remember(id string, grant *db.ConnectorOAuth) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.grants == nil {
		m.grants = map[string]*db.ConnectorOAuth{}
	}
	m.grants[id] = grant
}

// Forget drops a connector's cached grant (deleted or re-authorized elsewhere).
func (m *Manager) Forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.grants, id)
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
