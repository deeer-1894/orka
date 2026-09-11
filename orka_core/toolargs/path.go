// Package toolargs holds argument compatibility shared by tool execution and
// control-layer policies. It does not resolve paths or grant filesystem access.
package toolargs

import "strings"

// Path accepts the existing file tools' common synonyms in canonical precedence.
// Models sometimes change spelling mid-run; consumers must agree on the file
// being addressed. Empty/non-string values are skipped, with whitespace trimmed.
func Path(args map[string]any) string {
	for _, key := range [...]string{"path", "file", "filename", "file_path", "filepath"} {
		value, _ := args[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
