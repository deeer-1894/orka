package service

import (
	"bytes"
	"embed"
	"os"

	"github.com/orka-oss/orka_core/pathsafe"
)

// Phase-1 quant harness assets, embedded in the binary and seeded into a user's
// workspace quant/ folder on first pipeline use. This makes the real Python
// backtest/GP path work out of the box; the `backtest`/`gp_evolve` tools still
// fall back to the deterministic stub if Python isn't available.
//
//go:embed quant_assets/backtest_runner.py quant_assets/gp_evolve.py quant_assets/factor_schema.json
var quantAssets embed.FS

var quantAssetFiles = []string{"backtest_runner.py", "gp_evolve.py", "factor_schema.json"}

// seedQuantAssets writes the embedded harness into <workspaceRoot>/quant/ for
// any file that doesn't already exist (never clobbers user edits). Best-effort.
func seedQuantAssets(baseStorage, email string, conversationID string) {
	root, err := pathsafe.EnsureSession(baseStorage, email, conversationID)
	if err != nil {
		return
	}
	dir, err := pathsafe.Resolve(root, "quant")
	if err != nil {
		return
	}
	if os.MkdirAll(dir, pathsafe.WorkspaceDirMode) != nil {
		return
	}
	for _, name := range quantAssetFiles {
		data, err := quantAssets.ReadFile("quant_assets/" + name)
		if err != nil {
			continue
		}
		dst, err := pathsafe.Resolve(dir, name)
		if err != nil {
			continue
		}
		// These are OUR harness files (not user data), so overwrite when the
		// embedded version changes — otherwise a workspace seeded once would be
		// stuck on an old harness. Skip the rewrite only when identical (avoids
		// touching mtimes / invalidating the panel cache needlessly).
		if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
			continue
		}
		_ = os.WriteFile(dst, data, pathsafe.WorkspaceFileMode)
	}
}
