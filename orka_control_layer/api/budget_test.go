package api

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/orka-oss/orka_control_layer/service"
	"github.com/orka-oss/orka_core/config"
	"sync"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
)

// Explicit in-memory test dependency: API tests never need Mongo or user keys.
type apiBudgetLedger struct {
	mu        sync.Mutex
	accounts  map[string]db.UsageAccount
	snapshots map[string][]byte
}

func (f *apiBudgetLedger) Load(ctx context.Context, owner string) (db.UsageAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.accounts[owner]
	a.Entries = append([]db.UsageEntry(nil), a.Entries...)
	return a, ctx.Err()
}
func (f *apiBudgetLedger) CompareAndSwap(ctx context.Context, owner string, version int64, a db.UsageAccount) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts == nil {
		f.accounts = map[string]db.UsageAccount{}
	}
	if f.accounts[owner].Version != version {
		return false, nil
	}
	a.Version = version + 1
	a.Entries = append([]db.UsageEntry(nil), a.Entries...)
	f.accounts[owner] = a
	return true, nil
}

func (f *apiBudgetLedger) PutUsageSnapshot(ctx context.Context, owner, runID string, raw []byte, initial bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshots == nil {
		f.snapshots = map[string][]byte{}
	}
	key := owner + "\x00" + runID
	if initial && f.snapshots[key] != nil {
		return nil
	}
	f.snapshots[key] = append([]byte(nil), raw...)
	return nil
}
func (f *apiBudgetLedger) UsageSnapshot(ctx context.Context, owner, runID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.snapshots[owner+"\x00"+runID]
	if !ok {
		return nil, db.ErrNotFound
	}
	return append([]byte(nil), raw...), nil
}

func TestBudgetEndpointUsesAuthenticatedOwnerAndLedger(t *testing.T) {
	a := settingsAPI(t)
	ledger := a.Chat.UsageLedger
	session, err := service.NewBudgetSession(context.Background(), config.AgentConfig{}, service.TaskBudgetRequest{MaxTokens: 100}, ledger, "alice", "owned")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.ReserveUsage(context.Background(), "gui", "gui", 60); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", "bob", "alice"} {
		c := settingsRequest(owner, "")
		c.Params = param.Params{{Key: "run_id", Value: "owned"}}
		a.GetRunBudget(context.Background(), c)
		want := 200
		if owner == "" {
			want = 401
		} else if owner == "bob" {
			want = 404
		}
		if c.Response.StatusCode() != want {
			t.Fatalf("owner=%s status=%d %s", owner, c.Response.StatusCode(), c.Response.Body())
		}
		if owner == "alice" {
			var result struct {
				Data service.BudgetSnapshot `json:"data"`
			}
			if err = json.Unmarshal(c.Response.Body(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.ReservedTokens != 60 || result.Data.RemainingTokens != 40 || len(result.Data.Sources) != 1 {
				t.Fatalf("%s", c.Response.Body())
			}
		}
	}
}
