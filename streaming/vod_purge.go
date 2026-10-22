package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// The VOD sessions' idle horizon (the reaper closes a session no segment was
// fetched from for this long) and how often the reaper and the folders' sweep
// run (Story 8.9).
const (
	vodSessionIdle  = 90 * time.Second
	vodSessionSweep = 30 * time.Second
)

// purgeVODSessions removes the VOD sessions' folders this process no longer
// serves (their playlists list every segment, so a folder grows with its
// transcode): a closed session's at once (its ffmpeg was stopped as it
// closed), and one no session here has (a previous run's: the sessions are in
// memory) once it has not changed for idle (a session being opened writes its
// folder before its row is stored).
func purgeVODSessions(ctx context.Context, layout *cache.Layout, store session.Store, idle time.Duration, now time.Time, logger *slog.Logger) {
	dirs, err := layout.SessionDirs()
	if err != nil {
		logger.Warn("vod: listing the session folders failed", "err", err.Error(), "event", "vod_session_purge_failed")
		return
	}
	for _, d := range dirs {
		id, err := uuid.Parse(d.ID)
		if err != nil {
			continue
		}
		row, ok, err := store.Get(ctx, id)
		if err != nil {
			continue
		}
		reason := "closed"
		switch {
		case ok && row.ClosedAt == nil:
			continue
		case !ok && now.Sub(d.Changed) < idle:
			continue
		case !ok:
			reason = "orphaned"
		}
		if err := layout.PurgeSession(d.ID); err != nil {
			// a file still open (Windows): the next sweep tries again
			logger.Warn("vod: removing a session folder failed", "session_id", d.ID, "reason", reason,
				"err", err.Error(), "event", "vod_session_purge_failed")
			continue
		}
		logger.Info("vod: session folder removed", "session_id", d.ID, "reason", reason, "event", "vod_session_purged")
	}
}
