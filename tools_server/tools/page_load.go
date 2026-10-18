package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxPageBytes = 2 << 20

type loadedPage struct {
	URL         *url.URL
	Body        string
	ContentType string
}

type pageStatusError struct {
	URL    string
	Status int
}

func (e *pageStatusError) Error() string {
	return fmt.Sprintf("fetch failed: HTTP %d for %s", e.Status, e.URL)
}

// parsePageURL permits bare hosts only for the legacy fetch_url interface.
func parsePageURL(raw string, bareHost bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	if len(raw) > 2048 {
		return nil, fmt.Errorf("url exceeds 2048 bytes")
	}
	if bareHost && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("invalid url: use an absolute HTTP(S) URL without credentials")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid url port")
		}
	}
	u.Fragment = ""
	return u, nil
}

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// loadPage bounds the decoded HTTP body and reports read errors instead of
// returning partial success. origin restricts discovery redirects to its site.
func loadPage(ctx context.Context, u, origin *url.URL, maxBytes int64) (loadedPage, error) {
	return loadPageWithinBudget(ctx, u, origin, maxBytes, nil)
}

// The optional per-call budget includes redirects, not just top-level loads.
func loadPageWithinBudget(ctx context.Context, u, origin *url.URL, maxBytes int64, remaining *int) (loadedPage, error) {
	takeRequest := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if remaining != nil {
			if *remaining <= 0 {
				return fmt.Errorf("discovery request budget exhausted (maximum 5)")
			}
			*remaining--
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return loadedPage{}, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OrkaBot/0.1)")
	client := *httpSearchC
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return fmt.Errorf("too many redirects (maximum 3)")
		}
		if _, err := parsePageURL(req.URL.String(), false); err != nil {
			return err
		}
		if origin != nil && !sameOrigin(origin, req.URL) {
			return fmt.Errorf("index redirect leaves the supplied origin")
		}
		if httpSearchC.CheckRedirect != nil {
			if err := httpSearchC.CheckRedirect(req, via); err != nil {
				return err
			}
		}
		return takeRequest()
	}
	if err := takeRequest(); err != nil {
		return loadedPage{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return loadedPage{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return loadedPage{}, &pageStatusError{resp.Request.URL.String(), resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return loadedPage{}, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return loadedPage{}, fmt.Errorf("response body exceeds %d bytes; use a smaller page or index", maxBytes)
	}
	return loadedPage{resp.Request.URL, strings.ToValidUTF8(string(raw), "�"), resp.Header.Get("Content-Type")}, nil
}

// Preserve fetch_url's byte budget, but never split a UTF-8 sequence.
func truncatePageBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "…"
}

func truncatePageChars(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string([]rune(s)[:max-1]) + "…"
}
