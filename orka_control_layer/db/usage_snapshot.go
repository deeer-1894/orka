package db

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// UsageSnapshotStore persists immutable run policy and final usage projections.
// Active counters are always derived from UsageLedger, not this projection.
// Keeping one document per run avoids growing an owner's quota document forever.
// initial=true creates without overwriting an existing run's policy/deadline.
type UsageSnapshotStore interface {
	PutUsageSnapshot(context.Context, string, string, []byte, bool) error
	UsageSnapshot(context.Context, string, string) ([]byte, error)
}

func (l *mongoUsageLedger) snapshotCollection() (*mongo.Collection, error) {
	if l.store == nil || l.store.db == nil {
		return nil, errors.New("usage snapshot: storage unavailable")
	}
	return l.store.db.Collection("usage_run_budgets", options.Collection().SetWriteConcern(writeconcern.Majority())), nil
}
func usageSnapshotID(owner, runID string) bson.D {
	return bson.D{{Key: "owner", Value: owner}, {Key: "run_id", Value: runID}}
}
func (l *mongoUsageLedger) PutUsageSnapshot(ctx context.Context, owner, runID string, payload []byte, initial bool) error {
	if owner == "" || runID == "" || len(payload) == 0 {
		return errors.New("usage snapshot requires owner, run and payload")
	}
	col, err := l.snapshotCollection()
	if err != nil {
		return err
	}
	op := "$set"
	if initial {
		op = "$setOnInsert"
	}
	_, err = col.UpdateOne(ctx, bson.M{"_id": usageSnapshotID(owner, runID)}, bson.M{op: bson.M{"snapshot": payload}}, options.Update().SetUpsert(true))
	if initial && mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}
func (l *mongoUsageLedger) UsageSnapshot(ctx context.Context, owner, runID string) ([]byte, error) {
	col, err := l.snapshotCollection()
	if err != nil {
		return nil, err
	}
	var row struct {
		Snapshot []byte `bson:"snapshot"`
	}
	err = col.FindOne(ctx, bson.M{"_id": usageSnapshotID(owner, runID)}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return row.Snapshot, err
}

var _ UsageSnapshotStore = (*mongoUsageLedger)(nil)
