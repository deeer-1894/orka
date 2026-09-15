package db

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// The driver's mock deployment responds in memory; these tests do not discover
// a database, read a config file, or connect to user/deployment infrastructure.
func TestUsageMongoAtomicVersionAndBootstrap(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("bootstrap preserves legacy daily spend", func(mt *mtest.T) {
		ledger := NewUsageLedger(&Storage{db: mt.DB, Runs: mt.DB.Collection("runs")})
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".usage_accounts", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".runs", mtest.FirstBatch, bson.D{{Key: "total", Value: int64(120)}}),
		)
		a, err := ledger.Load(context.Background(), "fake@example.test")
		if err != nil || a.Version != 0 || len(a.Entries) != 1 || a.Entries[0].Tokens != 120 || a.Entries[0].Status != UsageEstimated {
			mt.Fatalf("bootstrap %+v %v", a, err)
		}
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))
		ok, err := ledger.CompareAndSwap(context.Background(), "fake@example.test", 0, a)
		if err != nil || !ok {
			mt.Fatalf("insert %v %v", ok, err)
		}
		mt.AddMockResponses(mtest.CreateWriteErrorsResponse(mtest.WriteError{Code: 11000, Message: "duplicate _id"}))
		ok, err = ledger.CompareAndSwap(context.Background(), "fake@example.test", 0, a)
		if err != nil || ok {
			mt.Fatalf("concurrent insert must lose CAS: %v %v", ok, err)
		}
	})
	mt.Run("version guards replacement", func(mt *mtest.T) {
		ledger := NewUsageLedger(&Storage{db: mt.DB})
		for _, matched := range []int{0, 1} {
			mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: matched}, bson.E{Key: "nModified", Value: matched}))
			ok, err := ledger.CompareAndSwap(context.Background(), "fake", 7, UsageAccount{})
			if err != nil || ok != (matched == 1) {
				mt.Fatalf("CAS %v %v", ok, err)
			}
			evt := mt.GetStartedEvent()
			if evt.CommandName != "update" {
				mt.Fatalf("command %s", evt.CommandName)
			}
			update := evt.Command.Lookup("updates").Array().Index(0).Value().Document()
			query := update.Lookup("q").Document()
			if query.Lookup("_id").StringValue() != "fake" || query.Lookup("version").Int64() != 7 {
				mt.Fatalf("missing atomic predicate: %s", query)
			}
			next := update.Lookup("u").Document()
			if next.Lookup("version").Int64() != 8 {
				mt.Fatalf("version not advanced: %s", next)
			}
		}
	})
	mt.Run("load persisted reservations without counting run summaries again", func(mt *mtest.T) {
		ledger := NewUsageLedger(&Storage{db: mt.DB})
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".usage_accounts", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: "fake"}, {Key: "version", Value: int64(3)},
			{Key: "entries", Value: bson.A{bson.D{{Key: "run_id", Value: "root"}, {Key: "call_id", Value: "inflight"}, {Key: "status", Value: UsageReserved}, {Key: "tokens", Value: 80}}}},
		}))
		a, err := ledger.Load(context.Background(), "fake")
		if err != nil || a.Version != 3 || len(a.Entries) != 1 || a.Entries[0].Tokens != 80 {
			mt.Fatalf("reload %+v %v", a, err)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 {
			mt.Fatalf("unexpected additional legacy query: %d", len(events))
		}
	})
	mt.Run("unreadable legacy usage fails closed", func(mt *mtest.T) {
		ledger := NewUsageLedger(&Storage{db: mt.DB, Runs: mt.DB.Collection("runs")})
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".usage_accounts", mtest.FirstBatch), mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Message: "forbidden"}))
		if _, err := ledger.Load(context.Background(), "fake"); err == nil {
			mt.Fatal("legacy failure became zero")
		}
	})
}

func TestUsageMongoMissingStorageIsExplicit(t *testing.T) {
	ledger := NewUsageLedger(nil)
	if _, err := ledger.Load(context.Background(), "fake"); err == nil {
		t.Fatal("missing storage silently allowed")
	}
	if _, err := ledger.CompareAndSwap(context.Background(), "fake", 0, UsageAccount{}); err == nil {
		t.Fatal("missing storage silently committed")
	}
}
