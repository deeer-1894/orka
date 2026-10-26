package connectoroauth

import (
	"os"
	"strings"
)

// Provider is one way to authorize a connector. A preset carries endpoints
// and the deployment's registered app; the "mcp" provider discovers
// everything from the MCP server itself (MCP authorization: protected
// resource metadata, authorization server metadata, dynamic client
// registration, PKCE), so it needs no registration and works for any server
// that follows the spec (Notion's hosted MCP among them).
type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	MCPURL       string `json:"mcp_url,omitempty"`
	Ready        bool   `json:"ready"`             // usable on this deployment
	Missing      string `json:"missing,omitempty"` // what the deployment still has to configure
	ScanToLogin  bool   `json:"scan_to_login,omitempty"`
	authorizeURL string
	tokenURL     string
	scopes       string
	clientID     string
	clientSecret string
	tokenStyle   tokenStyle
	discover     bool // MCP authorization discovery instead of fixed endpoints
}

type tokenStyle int

const (
	formToken tokenStyle = iota // application/x-www-form-urlencoded, RFC 6749
	jsonToken                   // JSON body (Feishu)
)

const ProviderMCP = "mcp"

// Providers lists what this deployment offers, presets first. Env:
//
//	ORKA_GITHUB_CLIENT_ID / ORKA_GITHUB_CLIENT_SECRET   GitHub OAuth app
//	ORKA_FEISHU_APP_ID / ORKA_FEISHU_APP_SECRET         Feishu self-built app
//	ORKA_FEISHU_MCP_URL                                 an MCP server that takes a Feishu user token
func Providers() []Provider {
	gh := Provider{
		ID: "github", Name: "GitHub", Description: "仓库、Issue、Pull Request",
		MCPURL:       "https://api.githubcopilot.com/mcp/",
		authorizeURL: "https://github.com/login/oauth/authorize",
		tokenURL:     "https://github.com/login/oauth/access_token",
		scopes:       "repo read:org read:user",
		clientID:     env("ORKA_GITHUB_CLIENT_ID"), clientSecret: env("ORKA_GITHUB_CLIENT_SECRET"),
	}
	gh.Ready, gh.Missing = ready(gh, "GitHub OAuth App（ORKA_GITHUB_CLIENT_ID / ORKA_GITHUB_CLIENT_SECRET）")

	notion := Provider{
		ID: "notion", Name: "Notion", Description: "页面、数据库",
		MCPURL: "https://mcp.notion.com/mcp", discover: true, Ready: true,
	}

	feishu := Provider{
		ID: "feishu", Name: "飞书", Description: "云文档、多维表格、消息、日历；支持飞书 App 扫码授权",
		MCPURL:       env("ORKA_FEISHU_MCP_URL"),
		authorizeURL: "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
		tokenURL:     "https://open.feishu.cn/open-apis/authen/v2/oauth/token",
		scopes:       "offline_access",
		clientID:     env("ORKA_FEISHU_APP_ID"), clientSecret: env("ORKA_FEISHU_APP_SECRET"),
		tokenStyle: jsonToken, ScanToLogin: true,
	}
	feishu.Ready, feishu.Missing = ready(feishu, "飞书企业自建应用（ORKA_FEISHU_APP_ID / ORKA_FEISHU_APP_SECRET）和飞书 MCP 服务地址（ORKA_FEISHU_MCP_URL）")

	generic := Provider{ID: ProviderMCP, Name: "其他 MCP 服务", Description: "任何支持标准 OAuth 授权的 MCP 服务，填入地址即可", discover: true, Ready: true}
	return []Provider{gh, notion, feishu, generic}
}

func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func ready(p Provider, needs string) (bool, string) {
	if p.clientID == "" || p.clientSecret == "" || p.MCPURL == "" {
		return false, "需要部署方配置" + needs
	}
	return true, ""
}

func env(k string) string { return strings.TrimSpace(os.Getenv(k)) }
