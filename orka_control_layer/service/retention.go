package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/pathsafe"
)

// Retention removes what has outlived its use so storage stops growing with
// every run. Fixed limits cover records nobody looks at again; conversations
// themselves are kept forever unless the deployment sets ORKA_RETENTION_DAYS.
type Retention struct {
	Store *db.Storage
	Base  string // base storage path (journals, session workspaces)
	Log   *slog.Logger
	// ConversationDays deletes conversations (with their messages, runs and
	// workspace) untouched for this long; 0 keeps them forever.
	ConversationDays int
}

const (
	notificationDays     = 90
	journalDays          = 30 // a run not resumed in a month will not be
	keepArtifactVersions = 20
	staleBatch           = 200
)

// RetentionDaysFromEnv reads ORKA_RETENTION_DAYS; unset, invalid or 0 keeps
// conversations forever.
func RetentionDaysFromEnv() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ORKA_RETENTION_DAYS")))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// RetentionReport counts what one sweep removed.
type RetentionReport struct {
	Notifications, ArtifactVersions, Journals, Runs, Conversations int64
}

// Sweep runs every rule once.
func (r *Retention) Sweep(ctx context.Context, now time.Time) (RetentionReport, error) {
	var rep RetentionReport
	var err error
	days := func(n int) int64 { return now.Add(-time.Duration(n) * 24 * time.Hour).UnixMilli() }
	if rep.Notifications, err = r.Store.DeleteNotificationsBefore(ctx, days(notificationDays)); err != nil {
		return rep, err
	}
	if rep.ArtifactVersions, err = r.Store.PruneArtifactVersions(ctx, keepArtifactVersions); err != nil {
		return rep, err
	}
	rep.Journals = r.sweepJournals(now.Add(-journalDays * 24 * time.Hour))
	if r.ConversationDays <= 0 {
		return rep, nil
	}
	cutoff := days(r.ConversationDays)
	if rep.Runs, err = r.Store.DeleteFinishedRunsBefore(ctx, cutoff); err != nil {
		return rep, err
	}
	stale, err := r.Store.StaleConversations(ctx, cutoff, staleBatch)
	if err != nil {
		return rep, err
	}
	for _, c := range stale {
		if err := r.Store.DeleteConversation(ctx, c.ConversationID); err != nil {
			return rep, err
		}
		if c.OwnerEmail != "" {
			if root, err := pathsafe.SessionRoot(r.Base, c.OwnerEmail, c.ConversationID); err == nil {
				_ = os.RemoveAll(root)
			}
		}
		rep.Conversations++
	}
	return rep, nil
}

func (r *Retention) sweepJournals(before time.Time) int64 {
	dir := filepath.Join(r.Base, ".orka_journals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(before) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}

// Start sweeps shortly after boot and then daily. Sweeps are idempotent, so
// several instances running them is harmless.
func (r *Retention) Start(ctx context.Context) {
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			rep, err := r.Sweep(ctx, time.Now())
			if err != nil {
				r.Log.Warn("retention sweep", "err", err)
			} else if rep != (RetentionReport{}) {
				r.Log.Info("retention sweep", "notifications", rep.Notifications, "artifact_versions", rep.ArtifactVersions,
					"journals", rep.Journals, "runs", rep.Runs, "conversations", rep.Conversations)
			}
			timer.Reset(24 * time.Hour)
		}
	}()
}
