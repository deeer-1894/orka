package db

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

var ErrScheduleClaimLost = errors.New("scheduled task claim no longer owned")

// ScheduleClaimTTL exceeds the conversation execution lease's TTL. Renewals
// keep long runs occupied; after a process crash, a later due scan can recover.
const ScheduleClaimTTL = 2 * time.Minute

// ClaimScheduledTask compares due time and installs an opaque lease token in
// one atomic write. An expired token can be replaced but never renewed or used
// to complete its successor. Side effects still require application idempotency.
func (s *Storage) ClaimScheduledTask(ctx context.Context, task TaskMeta, claimID string, now int64) (bool, error) {
	if claimID == "" || task.TaskID == "" {
		return false, errors.New("task and claim ID required")
	}
	if task.CronStatus != "on" || task.IntervalSec <= 0 || task.NextRunAt > now {
		return false, nil
	}
	if now < 0 || task.IntervalSec > (math.MaxInt64-now)/1000 {
		return false, errors.New("schedule interval overflow")
	}
	next := now + task.IntervalSec*1000
	filter := bson.M{"task_id": task.TaskID, "cron_status": "on", "interval_sec": task.IntervalSec, "next_run_at": task.NextRunAt,
		"$or": bson.A{bson.M{"schedule_claim": bson.M{"$exists": false}}, bson.M{"schedule_claim": ""}, bson.M{"schedule_lease_until": bson.M{"$lte": now}}},
	}
	res, err := s.Tasks.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"schedule_claim": claimID, "schedule_claimed_at": now, "schedule_overlap_policy": "skip", "schedule_lease_until": now + ScheduleClaimTTL.Milliseconds(),
		"next_run_at": next, "run_status": RunRunning,
	}})
	if err != nil {
		return false, fmt.Errorf("claim scheduled task: %w", err)
	}
	if res.MatchedCount == 1 {
		return true, nil
	}
	// An active task spans another due period: explicitly skip that period and
	// advance its due time. Repeated ticks on the same snapshot cannot count twice.
	_, err = s.Tasks.UpdateOne(ctx, bson.M{"task_id": task.TaskID, "cron_status": "on", "interval_sec": task.IntervalSec, "next_run_at": task.NextRunAt,
		"schedule_claim": bson.M{"$exists": true, "$ne": ""}, "schedule_lease_until": bson.M{"$gt": now},
	}, bson.M{"$set": bson.M{"next_run_at": next, "schedule_overlap_policy": "skip", "schedule_last_skip_at": now}, "$inc": bson.M{"schedule_skipped_runs": 1}})
	if err != nil {
		return false, fmt.Errorf("record schedule overlap: %w", err)
	}
	return false, nil
}

// CompleteScheduledTask only releases the matching owner token. It also skips
// any periods elapsed during execution, so completion cannot immediately replay
// a backlog. A stale completion cannot release or overwrite a newer run.
func (s *Storage) CompleteScheduledTask(ctx context.Context, task TaskMeta, claimID, status string, now int64) error {
	if claimID == "" {
		return ErrScheduleClaimLost
	}
	next := now
	if now >= 0 && task.IntervalSec > 0 && task.IntervalSec <= (math.MaxInt64-now)/1000 {
		next += task.IntervalSec * 1000
	}
	res, err := s.Tasks.UpdateOne(ctx, bson.M{"task_id": task.TaskID, "schedule_claim": claimID, "schedule_lease_until": bson.M{"$gt": now}}, bson.M{
		"$set": bson.M{"schedule_claim": "", "schedule_lease_until": 0, "schedule_finished_at": now, "schedule_last_status": status, "run_status": status},
		"$max": bson.M{"next_run_at": next},
	})
	if err != nil {
		return fmt.Errorf("complete scheduled task: %w", err)
	}
	if res.MatchedCount != 1 {
		return ErrScheduleClaimLost
	}
	return nil
}

// RenewScheduledTask refuses already-expired owners even before another process
// replaces them. This prevents reviving a lease after its safety deadline.
func (s *Storage) RenewScheduledTask(ctx context.Context, taskID, claimID string, now int64) (bool, error) {
	if taskID == "" || claimID == "" {
		return false, ErrScheduleClaimLost
	}
	res, err := s.Tasks.UpdateOne(ctx, bson.M{"task_id": taskID, "schedule_claim": claimID, "schedule_lease_until": bson.M{"$gt": now}}, bson.M{"$max": bson.M{"schedule_lease_until": now + ScheduleClaimTTL.Milliseconds()}})
	if err != nil {
		return false, fmt.Errorf("renew scheduled task: %w", err)
	}
	return res.MatchedCount == 1, nil
}
