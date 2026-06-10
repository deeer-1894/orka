// Package scheduled_task renders task templates and triggers cron-driven runs.
package scheduled_task

import (
	"fmt"
	"regexp"
)

var placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// Render substitutes {{key}} placeholders with values from vars. Unknown keys
// are left as the empty string.
func Render(tmpl string, vars map[string]any) string {
	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := placeholder.FindStringSubmatch(m)[1]
		if v, ok := vars[key]; ok {
			return fmt.Sprint(v)
		}
		return ""
	})
}
