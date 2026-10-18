package service

import (
	"context"
	"github.com/orka-oss/orka_core/config"
	"testing"
)

func TestLegacyAccountQuotaIsIgnored(t *testing.T) {
	for _, n := range []int{0, 1, 50_000_000, -1} {
		svc := &ChatService{Cfg: &config.Config{Agent: config.AgentConfig{UserDailyTokens: n}}, UsageLedger: &fakeLedger{}}
		ctx, cancel, err := svc.AuxiliaryBudgetContext(context.Background(), "u", "probe")
		if err != nil {
			t.Fatal(err)
		}
		meter := BudgetSessionFrom(ctx)
		if err := meter.ReserveUsage(ctx, "call", "probe", 100_000_000); err != nil {
			t.Fatal(err)
		}
		daily, err := meter.DailySnapshot(ctx)
		cancel()
		if err != nil || daily.LimitTokens != 0 || daily.ReservedTokens != 100_000_000 {
			t.Fatalf("usage %+v %v", daily, err)
		}
	}
}
