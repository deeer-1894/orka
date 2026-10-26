package db

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Retention deletes, in bounded batches, what has outlived its use. Each
// method returns how many records it removed.

// DeleteNotificationsBefore removes notifications created before cutoff (ms).
func (s *Storage) DeleteNotificationsBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.Notifications.DeleteMany(ctx, bson.M{"created_at": bson.M{"$lt": cutoff}})
	if err != nil {
		return 0, fmt.Errorf("delete notifications: %w", err)
	}
	return res.DeletedCount, nil
}

// PruneArtifactVersions keeps the newest keep versions of every artifact.
func (s *Storage) PruneArtifactVersions(ctx context.Context, keep int) (int64, error) {
	cur, err := s.ArtifactVers.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$artifact_id"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		{{Key: "$match", Value: bson.D{{Key: "n", Value: bson.D{{Key: "$gt", Value: keep}}}}}},
	})
	if err != nil {
		return 0, fmt.Errorf("artifact versions: %w", err)
	}
	var groups []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &groups); err != nil {
		return 0, fmt.Errorf("artifact versions: %w", err)
	}
	var removed int64
	for _, g := range groups {
		var boundary struct {
			Version int `bson:"version"`
		}
		// The newest version past the ones kept; it and older ones go.
		opts := options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}}).SetSkip(int64(keep))
		if err := s.ArtifactVers.FindOne(ctx, bson.M{"artifact_id": g.ID}, opts).Decode(&boundary); err != nil {
			continue
		}
		// A rolled-back artifact may point at an old version: never drop it.
		var art struct {
			Current int `bson:"current_version"`
		}
		_ = s.Artifacts.FindOne(ctx, bson.M{"artifact_id": g.ID}).Decode(&art)
		res, err := s.ArtifactVers.DeleteMany(ctx, bson.M{"artifact_id": g.ID, "version": bson.M{"$lte": boundary.Version, "$ne": art.Current}})
		if err != nil {
			return removed, fmt.Errorf("prune artifact versions: %w", err)
		}
		removed += res.DeletedCount
	}
	return removed, nil
}

// DeleteFinishedRunsBefore removes run records that finished before cutoff.
// Runs that are running, paused or waiting to resume are never touched.
func (s *Storage) DeleteFinishedRunsBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.Runs.DeleteMany(ctx, bson.M{
		"created_at": bson.M{"$lt": cutoff},
		"status":     bson.M{"$in": bson.A{"done", "partial", "failed", "cancelled", "canceled"}},
	})
	if err != nil {
		return 0, fmt.Errorf("delete runs: %w", err)
	}
	return res.DeletedCount, nil
}

// StaleConversations lists up to limit conversations with no message since
// cutoff that nothing else depends on: not filed in a project and not the
// home of a scheduled task.
func (s *Storage) StaleConversations(ctx context.Context, cutoff int64, limit int64) ([]ConversationTable, error) {
	taskConvs, err := s.Tasks.Distinct(ctx, "conversation_id", bson.M{})
	if err != nil {
		return nil, fmt.Errorf("task conversations: %w", err)
	}
	filter := bson.M{
		"created_at":      bson.M{"$lt": cutoff},
		"conversation_id": bson.M{"$nin": taskConvs},
		"$or":             bson.A{bson.M{"project_id": bson.M{"$exists": false}}, bson.M{"project_id": ""}},
	}
	cur, err := s.Conversations.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(limit*4))
	if err != nil {
		return nil, fmt.Errorf("stale conversations: %w", err)
	}
	var old []ConversationTable
	if err := cur.All(ctx, &old); err != nil {
		return nil, fmt.Errorf("stale conversations: %w", err)
	}
	if len(old) == 0 {
		return nil, nil
	}
	ids := make([]string, len(old))
	for i, c := range old {
		ids[i] = c.ConversationID
	}
	active, err := s.Messages.Distinct(ctx, "conversation_id", bson.M{"conversation_id": bson.M{"$in": ids}, "created_at": bson.M{"$gte": cutoff}})
	if err != nil {
		return nil, fmt.Errorf("active conversations: %w", err)
	}
	recent := map[string]bool{}
	for _, v := range active {
		if id, ok := v.(string); ok {
			recent[id] = true
		}
	}
	var out []ConversationTable
	for _, c := range old {
		if !recent[c.ConversationID] && int64(len(out)) < limit {
			out = append(out, c)
		}
	}
	return out, nil
}
