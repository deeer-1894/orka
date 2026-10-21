package modelsettings

import (
	"net/url"
	"strings"
)

// Endpoint is what a provider endpoint is known to need beyond the OpenAI
// wire format. The zero value means "unknown": send nothing extra and leave
// the provider's own defaults in place.
type Endpoint struct {
	// MaxOutputTokens replaces a provider default too small for writing files
	// in one tool call; per-model policies still override it.
	MaxOutputTokens int
}

// EndpointFor recognizes endpoints by host. Volcengine Ark caps the answer at
// 4096 tokens by default, separately from reasoning tokens, which truncated
// file-writing tool calls; 16384 is accepted by every preset model (checked
// 2026-10-20) and still leaves reasoning untouched.
func EndpointFor(base string) Endpoint {
	u, err := url.Parse(base)
	if err != nil {
		return Endpoint{}
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(host, "ark.") && strings.HasSuffix(host, ".volces.com") {
		return Endpoint{MaxOutputTokens: 16384}
	}
	return Endpoint{}
}
