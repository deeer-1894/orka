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

// Store is what the flow needs from persistence. Pending authorizations and
// grants live there, not in this process, so any instance can take a callback
// or refresh a token.
type Store interface {
	CreateConnector(ctx context.Context, c *db.Connector) error
	GetConnector(ctx context.Context, id, owner string) (*db.Connector, error)
	SaveConnectorOAuth(ctx context.Context, id, owner string, grant *db.ConnectorOAuth) error
	// SaveConnectorOAuthIf stores grant only while the stored refresh token
	// is still expectRefresh, and reports whether it did.
	SaveConnectorOAuthIf(ctx context.Context, id, owner, expectRefresh string, grant *db.ConnectorOAuth) (bool, error)
	SavePendingOAuth(ctx context.Context, p *db.OAuthPending) error
	TakePendingOAuth(ctx context.Context, state string) (*db.OAuthPending, error)
	CountPendingOAuth(ctx context.Context, owner string) (int64, error)
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
	grants  map[string]*db.ConnectorOAuth // latest grant this process saw per connector
	refresh map[string]*sync.Mutex        // one refresh per connector in this process
}

const (
	pendingTTL      = 10 * time.Minute
	refreshLeeway   = 2 * time.Minute
	maxPendingOwner = 20
	tokenBodyLimit  = 64 << 10
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
	if n, err := m.Store.CountPendingOAuth(ctx, r.Owner); err != nil {
		return "", err
	} else if n >= maxPendingOwner {
		return "", errors.New("授权请求过多，请稍后再试")
	}
	verifier, clientSecret := randomToken(48), p.clientSecret
	pa := &db.OAuthPending{Owner: r.Owner, Provider: p.ID, Name: p.Name, MCPURL: mcpURL, ConnectorID: r.ConnectorID, Redirect: r.Redirect,
		ClientID: p.clientID, TokenURL: p.tokenURL, Scopes: p.scopes, JSONToken: p.tokenStyle == jsonToken, ExpiresAt: m.now().Add(pendingTTL)}
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
		authorize, pa.TokenURL, pa.ClientID, clientSecret, pa.Scopes, pa.Resource = ep.authorize, ep.token, id, secret, ep.scopes, mcpURL
		if p.ID == ProviderMCP {
			if u, err := url.Parse(mcpURL); err == nil {
				pa.Name = u.Host
			}
		}
	}
	var err error
	if pa.Verifier, err = m.Sealer.Seal(verifier); err != nil {
		return "", err
	}
	if pa.ClientSecret, err = m.Sealer.Seal(clientSecret); err != nil {
		return "", err
	}
	pa.State = randomToken(32)
	if err := m.Store.SavePendingOAuth(ctx, pa); err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {pa.ClientID}, "redirect_uri": {r.Redirect}, "state": {pa.State},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	if pa.Scopes != "" {
		q.Set("scope", pa.Scopes)
	}
	if pa.Resource != "" {
		q.Set("resource", pa.Resource)
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
	if state == "" {
		return nil, errors.New("授权已过期，请重新连接")
	}
	pa, err := m.Store.TakePendingOAuth(ctx, state)
	if errors.Is(err, db.ErrNotFound) || (err == nil && !m.now().Before(pa.ExpiresAt)) {
		return nil, errors.New("授权已过期，请重新连接")
	}
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, errors.New("授权未完成")
	}
	verifier, err := m.Sealer.Open(pa.Verifier)
	if err != nil {
		return nil, err
	}
	clientSecret, err := m.Sealer.Open(pa.ClientSecret)
	if err != nil {
		return nil, err
	}
	style := formToken
	if pa.JSONToken {
		style = jsonToken
	}
	tok, err := m.exchange(ctx, style, pa.TokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {pa.Redirect},
		"client_id": {pa.ClientID}, "code_verifier": {verifier},
	}, clientSecret, pa.Resource)
	if err != nil {
		return nil, err
	}
	grant := &db.ConnectorOAuth{Provider: pa.Provider, TokenURL: pa.TokenURL, ClientID: pa.ClientID, Resource: pa.Resource, Scopes: tok.Scope}
	if err := m.seal(grant, tok, clientSecret); err != nil {
		return nil, err
	}
	grant.Account = m.account(ctx, pa.Provider, tok.AccessToken)
	cn := &db.Connector{ConnectorID: pa.ConnectorID, OwnerEmail: pa.Owner, Name: pa.Name, Transport: "streamable_http", URL: pa.MCPURL, Enabled: true, CreatedAt: m.now().UnixMilli(), OAuth: grant}
	if pa.ConnectorID != "" {
		if err := m.Store.SaveConnectorOAuth(ctx, pa.ConnectorID, pa.Owner, grant); err != nil {
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
//
// Several instances may hold the same connector. Before refreshing, the
// grant is reloaded (another instance may already have done it), and the
// refreshed grant is saved only if nobody rotated the refresh token in the
// meantime; the loser adopts the winner's grant instead of overwriting it.
func (m *Manager) Authorize(ctx context.Context, cn db.Connector) (map[string]string, error) {
	if cn.OAuth == nil {
		return nil, nil
	}
	lock := m.lockFor(cn.ConnectorID)
	lock.Lock()
	defer lock.Unlock()
	grant := m.latest(cn.ConnectorID, cn.OAuth)
	if m.dueForRefresh(grant) {
		if fresh, err := m.Store.GetConnector(ctx, cn.ConnectorID, cn.OwnerEmail); err == nil && fresh.OAuth != nil {
			grant = fresh.OAuth
			m.remember(cn.ConnectorID, grant)
		}
	}
	if grant.AuthError != "" {
		return nil, errors.New(grant.AuthError)
	}
	if m.dueForRefresh(grant) {
		save := context.WithoutCancel(ctx)
		next, err := m.refreshGrant(ctx, grant)
		if err != nil {
			failed := *grant
			failed.AuthError = "授权已失效，请重新授权"
			if ok, _ := m.Store.SaveConnectorOAuthIf(save, cn.ConnectorID, cn.OwnerEmail, grant.RefreshToken, &failed); ok {
				m.remember(cn.ConnectorID, &failed)
				return nil, err
			}
			// Someone else rotated the token while ours failed: use theirs.
			if next, err = m.reload(save, cn); err != nil {
				return nil, err
			}
		} else if ok, err := m.Store.SaveConnectorOAuthIf(save, cn.ConnectorID, cn.OwnerEmail, grant.RefreshToken, next); err != nil {
			return nil, err
		} else if !ok {
			if next, err = m.reload(save, cn); err != nil {
				return nil, err
			}
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

func (m *Manager) dueForRefresh(g *db.ConnectorOAuth) bool {
	return g.AuthError == "" && g.ExpiresAt > 0 && m.now().Add(refreshLeeway).UnixMilli() >= g.ExpiresAt
}

// reload takes the grant another instance stored after winning a refresh.
func (m *Manager) reload(ctx context.Context, cn db.Connector) (*db.ConnectorOAuth, error) {
	fresh, err := m.Store.GetConnector(ctx, cn.ConnectorID, cn.OwnerEmail)
	if err != nil {
		return nil, err
	}
	if fresh.OAuth == nil || fresh.OAuth.AuthError != "" {
		return nil, errors.New("授权已失效，请重新授权")
	}
	return fresh.OAuth, nil
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
