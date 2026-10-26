package scheduled_task

import (
	"github.com/orka-oss/orka_core/netguard"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
)

const watchBodyLimit = 2 << 20

// Observation is one look at a condition trigger's page.
type Observation struct {
	Fingerprint string
	Fire        bool
	Reason      string
}

var (
	scriptBlock = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	styleBlock  = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	noscript    = regexp.MustCompile(`(?is)<noscript\b.*?</noscript>`)
	tag         = regexp.MustCompile(`(?s)<[^>]*>`)
	space       = regexp.MustCompile(`\s+`)
)

// pageText keeps only visible text, so attribute churn does not count as change.
func pageText(body string) string {
	for _, re := range []*regexp.Regexp{scriptBlock, styleBlock, noscript} {
		body = re.ReplaceAllString(body, " ")
	}
	return strings.TrimSpace(space.ReplaceAllString(html.UnescapeString(tag.ReplaceAllString(body, " ")), " "))
}

// Observe fetches the watched page and decides whether the trigger fires.
// Without Contains the first look only records a baseline; with it, the task
// fires whenever the text is present and was not present last time.
func Observe(ctx context.Context, client *http.Client, t db.TaskTrigger) (Observation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.WatchURL, nil)
	if err != nil {
		return Observation{}, err
	}
	req.Header.Set("User-Agent", "Orka-Watch/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return Observation{}, fmt.Errorf("fetch watched page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Observation{}, fmt.Errorf("watched page returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, watchBodyLimit))
	if err != nil {
		return Observation{}, fmt.Errorf("read watched page: %w", err)
	}
	text := pageText(string(raw))
	if t.Contains != "" {
		present := strings.Contains(strings.ToLower(text), strings.ToLower(t.Contains))
		fp := "absent"
		if present {
			fp = "present"
		}
		return Observation{Fingerprint: fp, Fire: present && t.Fingerprint != "present",
			Reason: fmt.Sprintf("页面 %s 出现了“%s”。", t.WatchURL, t.Contains)}, nil
	}
	sum := sha256.Sum256([]byte(text))
	fp := hex.EncodeToString(sum[:])
	return Observation{Fingerprint: fp, Fire: t.Fingerprint != "" && fp != t.Fingerprint,
		Reason: fmt.Sprintf("页面 %s 的内容发生了变化。", t.WatchURL)}, nil
}

// WatchClient reaches only public addresses: watched URLs are user input, and
// the scheduler runs inside the deployment's network. Every dial, including
// redirects, resolves the host and refuses private, loopback and link-local IPs.
func WatchClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if !publicIP(ip.IP) {
						return nil, errors.New("watched URL resolves to a non-public address")
					}
				}
				if len(ips) == 0 {
					return nil, errors.New("watched host has no address")
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func publicIP(ip net.IP) bool { return netguard.IsPublic(ip) }
