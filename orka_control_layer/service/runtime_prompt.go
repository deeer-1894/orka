package service

import (
	"strings"
	"time"
)

// withRuntimeDate gives every model turn a stable calendar reference. Models
// otherwise tend to reuse an old year from a search query or cached example
// when a task says “latest” or “today”. It does not claim anything about the
// source being researched.
func withRuntimeDate(prompt string) string {
	date := time.Now().UTC().Format("2006-01-02")
	return strings.TrimSpace(prompt) + "\n\n[Runtime date]\nCurrent date (UTC): " + date + ". Resolve relative dates such as latest, today, and this year against this date; retrieve current sources instead of inferring them from memory."
}
