package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

const (
	// vodSessionIdle: the reaper closes a VOD session nothing was fetched from
	// for this long (a segment or a playlist: a paused player still reloads
	// the playlist of a transcode that is running; Story 8.9).
	vodSessionIdle = 90 * time.Second
	// vodSessionSweep: how often the reaper and the folders' sweep run.
	vodSessionSweep = 30 * time.Second
	// vodSessionGrace keeps the folder of a session closed for being idle: a
	// viewer back from a long pause plays on, and seeks within what was
	// transcoded, until then.
	vodSessionGrace = 10 * time.Minute
)

// transcodeRoom: the cache's disk has at least minFree free for a new
// transcode (0: no floor). A disk whose free space cannot be read does not
// stop one.
func transcodeRoom(root string, minFree uint64, logger *slog.Logger) error {
	if minFree == 0 {
		return nil
	}
	free, err := cache.FreeBytes(root)
	if err != nil {
		logger.Warn("vod: reading the cache disk's free space failed", "err", err.Error(), "event", "vod_disk_free_unknown")
		return nil
	}
	if free < minFree {
		logger.Warn("vod: too little free on the cache's disk to start a transcode", "free_bytes", free,
			"min_free_bytes", minFree, "event", "vod_disk_low")
		return fmt.Errorf("vod: %d bytes free on the cache's disk, under the %d floor", free, minFree)
	}
	return nil
}

// purgeVODSessions removes the VOD sessions' folders this process no longer
// serves (their playlists list every segment, so a folder grows with its
// transcode): a closed session's at once (its ffmpeg was stopped as it
// closed), one closed for being idle after vodSessionGrace, and one no session
// here has (a previous run's: the sessions are in memory) once it has not
// changed for vodSessionIdle (a session being opened writes its folder before
// its row is stored).
func purgeVODSessions(ctx context.Context, layout *cache.Layout, store session.Store, now time.Time, logger *slog.Logger) {
	ids, err := layout.SessionDirs()
	if err != nil {
		logger.Warn("vod: listing the session folders failed", "err", err.Error(), "event", "vod_session_purge_failed")
		return
	}
	for _, name := range ids {
		id, err := uuid.Parse(name)
		if err != nil {
			continue
		}
		row, ok, err := store.Get(ctx, id)
		if err != nil {
			continue
		}
		var reason string
		switch {
		case ok && row.ClosedAt == nil:
			continue
		case ok && row.ClosedReason == session.ReasonIdle:
			if now.Sub(*row.ClosedAt) < vodSessionGrace {
				continue
			}
			reason = "idle"
		case ok:
			reason = "closed"
		case now.Sub(layout.SessionChanged(name)) < vodSessionIdle:
			continue
		default:
			reason = "orphaned"
		}
		if err := layout.PurgeSession(name); err != nil {
			// a file still open (Windows): the next sweep tries again
			logger.Warn("vod: removing a session folder failed", "session_id", name, "reason", reason,
				"err", err.Error(), "event", "vod_session_purge_failed")
			continue
		}
		logger.Info("vod: session folder removed", "session_id", name, "reason", reason, "event", "vod_session_purged")
	}
}
