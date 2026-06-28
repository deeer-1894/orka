package service

import (
	"bytes"
	"embed"
	"os"
	"path/filepath"

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
func seedQuantAssets(baseStorage, email string) {
	dir := filepath.Join(pathsafe.UserRoot(baseStorage, email), "quant")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	for _, name := range quantAssetFiles {
		data, err := quantAssets.ReadFile("quant_assets/" + name)
		if err != nil {
			continue
		}
		dst := filepath.Join(dir, name)
		// These are OUR harness files (not user data), so overwrite when the
		// embedded version changes — otherwise a workspace seeded once would be
		// stuck on an old harness. Skip the rewrite only when identical (avoids
		// touching mtimes / invalidating the panel cache needlessly).
		if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
			continue
		}
		_ = os.WriteFile(dst, data, 0o644)
	}
}
