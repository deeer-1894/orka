package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var httpReqClient = &http.Client{
	Timeout: 20 * time.Second,
	// Block redirects to internal targets (SSRF via redirect).
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return guardURL(r.URL)
	},
	// Validate the IP we actually CONNECT to (not just the pre-flight DNS
	// lookup). This closes the DNS-rebinding TOCTOU window where a low-TTL host
	// passes guardURL as public, then re-resolves to an internal IP on dial.
	Transport: &http.Transport{
		DialContext: guardedDial,
	},
}

// guardedDial resolves the host itself, rejects any non-public candidate IP,
// and dials a vetted IP directly — so the connection cannot land on an internal
// address even if DNS changes between the guard check and the dial.
func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	var lastErr error
	for _, ip := range ips {
		if !isPublicIP(ip) {
			lastErr = fmt.Errorf("blocked: host resolves to a non-public address (%s)", ip)
			continue
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no dialable address for %s", host)
	}
	return nil, lastErr
}

// httpRequest performs a generic outbound HTTP GET/POST and returns the status
// and (truncated) body. It is SSRF-guarded: only http/https to public hosts are
// allowed — loopback, private, link-local and metadata IPs are rejected.
func httpRequest() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw := strings.TrimSpace(req.GetString("url", ""))
		if raw == "" {
			return mcp.NewToolResultError("url is required"), nil
		}
		method := strings.ToUpper(strings.TrimSpace(req.GetString("method", "GET")))
		if method != http.MethodGet && method != http.MethodPost {
			return mcp.NewToolResultError("method must be GET or POST"), nil
		}
		u, err := url.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid url: " + err.Error()), nil
		}
		if err := guardURL(u); err != nil {
			return mcp.NewToolResultError("blocked: " + err.Error()), nil
		}

		var bodyReader io.Reader
		if b := req.GetString("body", ""); b != "" {
			bodyReader = strings.NewReader(b)
		}
		hreq, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		hreq.Header.Set("User-Agent", "OrkaBot/0.1")
		if ct := req.GetString("content_type", ""); ct != "" {
			hreq.Header.Set("Content-Type", ct)
		} else if method == http.MethodPost {
			hreq.Header.Set("Content-Type", "application/json")
		}

		resp, err := httpReqClient.Do(hreq)
		if err != nil {
			return mcp.NewToolResultError("request failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // cap at 64KB
		out := fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
		return mcp.NewToolResultText(out), nil
	}
}

// guardURL rejects non-http(s) schemes and any host that resolves to a
// non-public IP (loopback / private / link-local / unspecified / multicast).
func guardURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("host resolves to a non-public address (%s)", ip)
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// Block the cloud metadata endpoint explicitly (defense in depth).
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return false
	}
	return true
}
