package connectoroauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// endpoints is what discovery (or a preset) yields for one authorization.
type endpoints struct {
	authorize, token, registration string
	scopes                         string
}

var resourceMetadataRe = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// discover follows MCP authorization: the server's protected resource
// metadata names its authorization server, whose metadata names the
// endpoints. Older servers without resource metadata are their own
// authorization server.
func discover(ctx context.Context, client *http.Client, mcpURL string) (endpoints, error) {
	u, err := url.Parse(mcpURL)
	if err != nil {
		return endpoints{}, err
	}
	origin := u.Scheme + "://" + u.Host
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	candidates := []string{}
	if challenge := probeChallenge(ctx, client, mcpURL); challenge != "" {
		candidates = append(candidates, challenge)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+strings.TrimRight(u.Path, "/"), origin+"/.well-known/oauth-protected-resource")
	issuer := origin
	for _, c := range candidates {
		if getJSON(ctx, client, c, &prm) == nil && len(prm.AuthorizationServers) > 0 {
			issuer = prm.AuthorizationServers[0]
			break
		}
	}
	iu, err := url.Parse(issuer)
	if err != nil {
		return endpoints{}, fmt.Errorf("authorization server: %w", err)
	}
	var meta struct {
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
	iorigin, ipath := iu.Scheme+"://"+iu.Host, strings.TrimRight(iu.Path, "/")
	for _, c := range []string{iorigin + "/.well-known/oauth-authorization-server" + ipath, iorigin + "/.well-known/openid-configuration" + ipath, issuer + "/.well-known/openid-configuration"} {
		if getJSON(ctx, client, c, &meta) == nil && meta.AuthorizationEndpoint != "" && meta.TokenEndpoint != "" {
			scopes := prm.ScopesSupported
			if len(scopes) == 0 {
				scopes = nil // ask for nothing in particular: the server's default grant
			}
			return endpoints{authorize: meta.AuthorizationEndpoint, token: meta.TokenEndpoint, registration: meta.RegistrationEndpoint, scopes: strings.Join(scopes, " ")}, nil
		}
	}
	return endpoints{}, errors.New("这个服务没有公开标准的 OAuth 授权信息，请改用手动添加并填写令牌")
}

// probeChallenge asks the server unauthenticated and reads the metadata
// location from its 401 challenge, when it gives one.
func probeChallenge(ctx context.Context, client *http.Client, mcpURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mcpURL, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return ""
	}
	if m := resourceMetadataRe.FindStringSubmatch(resp.Header.Get("WWW-Authenticate")); m != nil {
		return m[1]
	}
	return ""
}

// register performs dynamic client registration (RFC 7591) for a public
// client using PKCE.
func register(ctx context.Context, client *http.Client, endpoint, redirect string) (id, secret string, err error) {
	body, _ := json.Marshal(map[string]any{
		"client_name": "Orka", "redirect_uris": []string{redirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("register client: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if resp.StatusCode/100 != 2 || json.Unmarshal(raw, &out) != nil || out.ClientID == "" {
		return "", "", fmt.Errorf("register client: HTTP %d", resp.StatusCode)
	}
	return out.ClientID, out.ClientSecret, nil
}

func getJSON(ctx context.Context, client *http.Client, link string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(into)
}
