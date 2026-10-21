package db

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ScheduleRunAutoResume arranges for a resumable run to be continued at `at`
// (unix millis). Only a run whose recovery offer is still open qualifies, so a
// run that was resumed or cleared in the meantime is left alone.
func (s *Storage) ScheduleRunAutoResume(ctx context.Context, runID string, at int64, attempt int) (bool, error) {
	result, err := s.Runs.UpdateOne(ctx, bson.M{"run_id": runID, "resumable": true},
		bson.M{"$set": bson.M{"auto_resume_at": at, "auto_resume_attempt": attempt}})
	if err != nil {
		return false, fmt.Errorf("schedule run auto resume: %w", err)
	}
	return result.MatchedCount == 1, nil
}

// ClaimDueAutoResume atomically takes one due automatic resume, returning the
// run as it was before the claim. Unsetting the due date is the claim, so any
// number of control-plane instances can sweep concurrently and each pending
// resume still starts once. Returns ErrNotFound when nothing is due.
func (s *Storage) ClaimDueAutoResume(ctx context.Context, now int64) (*RunRecord, error) {
	var out RunRecord
	err := s.Runs.FindOneAndUpdate(ctx,
		bson.M{"auto_resume_at": bson.M{"$gt": 0, "$lte": now}, "resumable": true},
		bson.M{"$unset": bson.M{"auto_resume_at": ""}},
		options.FindOneAndUpdate().SetSort(bson.M{"auto_resume_at": 1}).SetReturnDocument(options.Before),
	).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("claim due auto resume: %w", err)
	}
	return &out, nil
}

// CancelRunAutoResume drops a pending automatic resume on the owner's request.
// The run stays resumable by hand. Reports whether anything was pending.
func (s *Storage) CancelRunAutoResume(ctx context.Context, runID, owner string) (bool, error) {
	result, err := s.Runs.UpdateOne(ctx,
		bson.M{"run_id": runID, "owner_email": owner, "auto_resume_at": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"auto_resume_at": ""}})
	if err != nil {
		return false, fmt.Errorf("cancel run auto resume: %w", err)
	}
	return result.ModifiedCount == 1, nil
}

// CancelConversationAutoResumes drops pending automatic resumes in a
// conversation. A new execution there supersedes them: continuing an older
// run after the user has moved on would answer a question nobody is asking.
func (s *Storage) CancelConversationAutoResumes(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	_, err := s.Runs.UpdateMany(ctx,
		bson.M{"conversation_id": conversationID, "auto_resume_at": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"auto_resume_at": ""}})
	if err != nil {
		return fmt.Errorf("cancel conversation auto resumes: %w", err)
	}
	return nil
}
