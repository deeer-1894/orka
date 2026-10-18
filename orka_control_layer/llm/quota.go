package llm

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var quotaResetPattern = regexp.MustCompile(`(?i)reset at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4})`)

// QuotaExhaustion distinguishes explicit account/billing exhaustion from burst
// throttling. Unknown 429 codes remain retryable. Parse a reset time only from a
// recognized quota error; never expose the provider's raw message or request ID.
func QuotaExhaustion(err error) (reset time.Time, exhausted bool) {
	var api *APIError
	if !errors.As(err, &api) || (api.Status != 429 && api.Status != 402) || len(api.Body) > 64<<10 {
		return reset, false
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(api.Body), &body) != nil {
		return reset, false
	}
	isQuota := func(code string) bool {
		switch strings.ToLower(code) {
		case "accountquotaexceeded", "insufficient_quota", "billing_hard_limit_reached":
			return true
		}
		return false
	}
	if !isQuota(body.Error.Code) && !isQuota(body.Error.Type) {
		return reset, false
	}
	if match := quotaResetPattern.FindStringSubmatch(body.Error.Message); len(match) == 2 {
		reset, _ = time.Parse("2006-01-02 15:04:05 -0700", match[1])
	}
	return reset, true
}
