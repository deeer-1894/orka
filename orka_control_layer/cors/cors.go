// Package cors implements an exact-host allowlist CORS policy. Origins are
// parsed and matched on hostname against a set — never substring-matched.
package cors

import (
	"context"
	"net/url"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// AllowedHost reports whether origin's hostname is in the allowlist.
// Pure function: unit-testable without an HTTP server.
func AllowedHost(origin string, allowed map[string]bool) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	return allowed[host]
}

// Middleware returns a Hertz handler enforcing the exact-host allowlist.
func Middleware(hosts []string) app.HandlerFunc {
	allowed := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		allowed[h] = true
	}
	return func(ctx context.Context, c *app.RequestContext) {
		origin := string(c.GetHeader("Origin"))
		if AllowedHost(origin, allowed) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization,X-User-Email")
			c.Header("Vary", "Origin")
		}
		if string(c.Method()) == consts.MethodOptions {
			c.AbortWithStatus(consts.StatusNoContent)
			return
		}
		c.Next(ctx)
	}
}
