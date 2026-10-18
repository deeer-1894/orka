package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/messages"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Explicit opt-in; every test uses a fresh test-only DB and never accesses the
// application's configured database. Cleanup drops only the generated test DB.
func isolatedWorkflowDB(t *testing.T) *Storage {
	t.Helper()
	uri := os.Getenv("ORKA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ORKA_TEST_MONGO_URI for isolated Mongo integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	database := client.Database("orka_workflow_test_" + messages.NewID())
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := database.Drop(ctx); err != nil {
			t.Error(err)
		}
		client.Disconnect(ctx)
	})
	return &Storage{Tasks: database.Collection("tasks"), Workflows: database.Collection("workflows")}
}
func TestScheduleMongoCASAndCrossPeriod(t *testing.T) {
	store := isolatedWorkflowDB(t)
	ctx := context.Background()
	task := TaskMeta{TaskID: "task", CronStatus: "on", IntervalSec: 1, NextRunAt: 1000}
	if _, err := store.Tasks.InsertOne(ctx, task); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var winner string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := messages.NewID()
			ok, err := store.ClaimScheduledTask(ctx, task, id, 1000)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
				mu.Lock()
				winner = id
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners=%d", wins.Load())
	}
	later := task
	later.NextRunAt = 2000
	if ok, err := store.ClaimScheduledTask(ctx, later, "overlap", 3000); ok || err != nil {
		t.Fatalf("cross-period reentry: %v %v", ok, err)
	}
	if err := store.CompleteScheduledTask(ctx, task, "stale", RunDone, 3000); !errors.Is(err, ErrScheduleClaimLost) {
		t.Fatalf("stale completion: %v", err)
	}
	var active struct {
		Claim   string `bson:"schedule_claim"`
		Skipped int    `bson:"schedule_skipped_runs"`
		Policy  string `bson:"schedule_overlap_policy"`
	}
	if err := store.Tasks.FindOne(ctx, bson.M{"task_id": "task"}).Decode(&active); err != nil {
		t.Fatal(err)
	}
	if active.Claim != winner || active.Skipped != 1 || active.Policy != "skip" {
		t.Fatalf("claim/overlap not durable: %+v", active)
	}
	if err := store.CompleteScheduledTask(ctx, task, winner, RunPartial, 5000); err != nil {
		t.Fatal(err)
	}
	// Finish skips elapsed periods rather than immediately firing a backlog.
	var finished TaskMeta
	if err := store.Tasks.FindOne(ctx, bson.M{"task_id": "task"}).Decode(&finished); err != nil {
		t.Fatal(err)
	}
	if finished.NextRunAt < 6000 || finished.RunStatus != RunPartial {
		t.Fatalf("completion: %+v", finished)
	}
	if ok, err := store.ClaimScheduledTask(ctx, finished, "new", 6000); !ok || err != nil {
		t.Fatalf("next period did not run: %v %v", ok, err)
	}
	if err := store.CompleteScheduledTask(ctx, task, winner, RunDone, 7000); !errors.Is(err, ErrScheduleClaimLost) {
		t.Fatalf("old completion overwrote new run: %v", err)
	}
}
func TestScheduleMongoDisabledAndNotDue(t *testing.T) {
	store := isolatedWorkflowDB(t)
	ctx := context.Background()
	task := TaskMeta{TaskID: "task", CronStatus: "on", IntervalSec: 1, NextRunAt: 2000}
	if _, err := store.Tasks.InsertOne(ctx, task); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimScheduledTask(ctx, task, "early", 1000); ok || err != nil {
		t.Fatalf("claimed early: %v %v", ok, err)
	}
	if _, err := store.Tasks.UpdateOne(ctx, bson.M{"task_id": "task"}, bson.M{"$set": bson.M{"cron_status": "off"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimScheduledTask(ctx, task, "disabled", 3000); ok || err != nil {
		t.Fatalf("claimed disabled: %v %v", ok, err)
	}
}

func TestScheduleMongoExpiredClaimCanRecover(t *testing.T) {
	store := isolatedWorkflowDB(t)
	ctx := context.Background()
	task := TaskMeta{TaskID: "task", CronStatus: "on", IntervalSec: 1, NextRunAt: 1000}
	if _, err := store.Tasks.InsertOne(ctx, task); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimScheduledTask(ctx, task, "crashed", 1000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	later := task
	later.NextRunAt = 2000
	if ok, err := store.ClaimScheduledTask(ctx, later, "successor", 1000000); !ok || err != nil {
		t.Fatalf("expired claim permanently locked: %v %v", ok, err)
	}
	if err := store.CompleteScheduledTask(ctx, task, "crashed", RunDone, 1000001); !errors.Is(err, ErrScheduleClaimLost) {
		t.Fatalf("old completion accepted: %v", err)
	}
}

func TestScheduleMongoRenewalPreventsCrossPeriodOverlap(t *testing.T) {
	store := isolatedWorkflowDB(t)
	ctx := context.Background()
	task := TaskMeta{TaskID: "task", CronStatus: "on", IntervalSec: 1, NextRunAt: 1000}
	if _, err := store.Tasks.InsertOne(ctx, task); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimScheduledTask(ctx, task, "long", 1000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := store.RenewScheduledTask(ctx, "task", "long", 100000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	later := task
	later.NextRunAt = 2000
	if ok, err := store.ClaimScheduledTask(ctx, later, "overlap", 150000); ok || err != nil {
		t.Fatalf("renewed task reentered: %v %v", ok, err)
	}
	if ok, err := store.RenewScheduledTask(ctx, "task", "long", 230000); ok || err != nil {
		t.Fatalf("expired lease revived: %v %v", ok, err)
	}
	if err := store.CompleteScheduledTask(ctx, task, "long", RunDone, 230000); !errors.Is(err, ErrScheduleClaimLost) {
		t.Fatalf("expired owner completed: %v", err)
	}
	later.NextRunAt = 151000
	if ok, err := store.ClaimScheduledTask(ctx, later, "successor", 230000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := store.RenewScheduledTask(ctx, "task", "long", 230001); ok || err != nil {
		t.Fatalf("old owner renewed successor: %v %v", ok, err)
	}
}
