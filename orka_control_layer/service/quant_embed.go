package service

import (
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
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // present already → leave it
		}
		data, err := quantAssets.ReadFile("quant_assets/" + name)
		if err != nil {
			continue
		}
		_ = os.WriteFile(dst, data, 0o644)
	}
}
