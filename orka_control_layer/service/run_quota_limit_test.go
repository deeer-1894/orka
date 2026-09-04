package service

import (
	"testing"

	"github.com/orka-oss/orka_core/config"
)

// The refusal tells the user to raise the ceiling "in config". That was a lie
// while the ceiling was a compile-time constant.
func TestDailyLimitHonoursConfig(t *testing.T) {
	s := &ChatService{Cfg: &config.Config{}}
	if got := s.dailyTokenLimit(); got != userDailyTokens {
		t.Errorf("unset config should fall back to the default, got %d", got)
	}

	s.Cfg.Agent.UserDailyTokens = 12_000_000
	if got := s.dailyTokenLimit(); got != 12_000_000 {
		t.Errorf("configured ceiling ignored, got %d", got)
	}

	// Zero means "unset", not "block everything" — a config that accidentally
	// zeroed this would otherwise refuse every run.
	s.Cfg.Agent.UserDailyTokens = 0
	if got := s.dailyTokenLimit(); got != userDailyTokens {
		t.Errorf("zero must mean unset, got %d", got)
	}
}

// A daily ceiling has to clear the largest run the deployment supports, several
// times over. A full research run measured 1.3M here, and the old 5M default
// allowed fewer than four of them.
func TestDailyLimitClearsAFullResearchRun(t *testing.T) {
	const measuredLargeRun = 1_300_000
	if userDailyTokens < 10*measuredLargeRun {
		t.Errorf("daily ceiling %d leaves room for only %d runs of %d tokens",
			userDailyTokens, userDailyTokens/measuredLargeRun, measuredLargeRun)
	}
	if userDailyTokens < runMaxTokens {
		t.Errorf("daily ceiling %d is below the single-run cap %d: no run could finish",
			userDailyTokens, runMaxTokens)
	}
}
