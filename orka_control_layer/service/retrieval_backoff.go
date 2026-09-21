package service

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Backoff is per transport destination, shared by read-only retrieval tools.
// It expires; a failure on one HTTP path is not proof its whole host is down.
type retrievalBackoff struct {
	Until  time.Time
	Reason string
}

const retrievalRetryDelay = 60 * time.Second

func retrievalPageKey(args map[string]any) string {
	raw, _ := args["url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.RawQuery = u.Query().Encode()
	return u.String()
}
func (s *researchSession) retrievalBackoffLocked(name string, args map[string]any) string {
	host := researchHost(name, args)
	if host == "" {
		return ""
	}
	now := s.now()
	for _, key := range []string{"host:" + host, "page:" + retrievalPageKey(args)} {
		b, ok := s.blockedHosts[key]
		if !ok {
			continue
		}
		if !now.Before(b.Until) {
			delete(s.blockedHosts, key)
			continue
		}
		return fmt.Sprintf("Recent %s; retry is deferred for %ds, not permanently disabled.", b.Reason, int(b.Until.Sub(now).Seconds())+1)
	}
	return ""
}
func (s *researchSession) recordRetrievalFailureLocked(name string, args map[string]any, message string) {
	host := researchHost(name, args)
	if host == "" {
		return
	}
	key, reason := "page:"+retrievalPageKey(args), "failure on this URL"
	low := strings.ToLower(message)
	for _, marker := range []string{"deadline exceeded", "timed out", "timeout awaiting", "dial tcp", "no such host", "connection refused", "network is unreachable"} {
		if strings.Contains(low, marker) {
			key, reason = "host:"+host, "transport timeout or connection failure for "+host
			break
		}
	}
	s.blockedHosts[key] = retrievalBackoff{Until: s.now().Add(retrievalRetryDelay), Reason: reason}
}
