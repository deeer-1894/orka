package tools

import (
	"github.com/orka-oss/orka_core/workspaceio"
	"strings"
)

// This adapter retains generator diagnostics while workspaceio owns staging,
// mandatory backup of existing outputs, cleanup and the atomic replacement.
func generateWorkspaceFile(root, out string, generate func(string) (string, error)) (string, error) {
	var output, temporary string
	result, err := workspaceio.ReplaceGenerated(root, out, func(temp string) error {
		temporary = temp
		var err error
		output, err = generate(temp)
		return err
	})
	if temporary != "" {
		output = strings.ReplaceAll(output, temporary, out)
	}
	if err != nil {
		return output, err
	}
	return strings.TrimSpace(output) + "\n" + result.String(), nil
}
