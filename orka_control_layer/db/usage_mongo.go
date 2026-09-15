package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// NewUsageLedger needs no startup migration or replica-set transactions: each
// owner's admission and settlement use one versioned Mongo document and its
// built-in unique _id. A database failure must be surfaced to admission callers.
func NewUsageLedger(store *Storage) UsageLedger { return &mongoUsageLedger{store: store} }

type mongoUsageLedger struct{ store *Storage }

func (l *mongoUsageLedger) collection() (*mongo.Collection, error) {
	if l.store == nil || l.store.db == nil {
		return nil, errors.New("usage ledger: storage unavailable")
	}
	return l.store.db.Collection("usage_accounts", options.Collection().SetWriteConcern(writeconcern.Majority())), nil
}

func (l *mongoUsageLedger) Load(ctx context.Context, owner string) (UsageAccount, error) {
	col, err := l.collection()
	if err != nil {
		return UsageAccount{}, err
	}
	var a UsageAccount
	err = col.FindOne(ctx, bson.M{"_id": owner}).Decode(&a)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return a, fmt.Errorf("load usage: %w", err)
	}
	a = UsageAccount{Owner: owner}
	// Bootstrap pre-ledger spend once. Holding the aggregate for 24h from
	// cutover is intentionally conservative; later run summaries are NOT added
	// again because all new attempts have individual ledger entries.
	// Deployments must retire old unmetered writers before enabling this ledger.
	now := time.Now()
	used, err := l.store.TokensSince(ctx, owner, now.Add(-24*time.Hour).UnixMilli())
	if err != nil {
		return a, fmt.Errorf("bootstrap usage: %w", err)
	}
	if used < 0 {
		return a, errors.New("bootstrap usage: negative legacy spend")
	}
	if used > 0 {
		a.Entries = []UsageEntry{{RunID: "legacy", CallID: "cutover", Source: "legacy_runs", Status: UsageEstimated, Tokens: used, SettledAt: now.UnixMilli()}}
	}
	return a, nil
}

func (l *mongoUsageLedger) CompareAndSwap(ctx context.Context, owner string, version int64, next UsageAccount) (bool, error) {
	col, err := l.collection()
	if err != nil {
		return false, err
	}
	if owner == "" || version < 0 || version == 1<<63-1 {
		return false, errors.New("usage ledger: invalid owner or version")
	}
	next.Owner = owner
	next.Version = version + 1
	if version == 0 {
		_, err = col.InsertOne(ctx, next)
		if mongo.IsDuplicateKeyError(err) {
			return false, nil
		}
		return err == nil, err
	}
	result, err := col.ReplaceOne(ctx, bson.M{"_id": owner, "version": version}, next)
	if err != nil {
		return false, fmt.Errorf("commit usage: %w", err)
	}
	return result.MatchedCount == 1, nil
}

var _ UsageLedger = (*mongoUsageLedger)(nil)
