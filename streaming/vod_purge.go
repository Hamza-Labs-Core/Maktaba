package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

const (
	// vodSessionIdle: the reaper closes a VOD session nothing was fetched from
	// for this long (a segment, or a playlist while its transcode runs: a
	// paused player still reloads it; Story 8.9).
	vodSessionIdle = 90 * time.Second
	// vodActiveIdle is that horizon while the session's transcode runs: a
	// player in a background tab or on a locked phone fetches nothing for a
	// while, and coming back finds its transcode going on.
	vodActiveIdle = 10 * time.Minute
	// vodSessionSweep: how often the reaper and the folders' sweep run.
	vodSessionSweep = 30 * time.Second
	// vodSessionGrace keeps the folder of a session closed for being idle,
	// after its last fetch, when its transcode was cut short (its player opens
	// the video again, in a new session).
	vodSessionGrace = 10 * time.Minute
	// vodFinishedGrace keeps it when its transcode was finished: a viewer back
	// from a long pause plays on, and seeks in all of it.
	vodFinishedGrace = 4 * time.Hour
)

// The automatic floor of free space on the cache's disk (vodDisk): a tenth of
// the disk, at most 20 GiB, at least 3 GiB.
const (
	gib             = 1 << 30
	autoFloorMax    = 20 * gib
	autoFloorMin    = 3 * gib
	autoFloorDivide = 10
)

// vodDisk is the cache's disk as the transcodes see it.
type vodDisk struct {
	root string
	// minFreeGiB is the configured floor (config.TranscodeConfig.MinFreeGiB):
	// that many GiB, 0 automatic, below 0 none.
	minFreeGiB int
	diskSpace  func(path string) (free, total uint64, err error) // nil: cache.DiskSpace
}

// space is the free space on the disk and the floor below which no transcode
// starts and the sweep frees what it keeps (0: none).
func (d vodDisk) space() (free, floor uint64, err error) {
	diskSpace := d.diskSpace
	if diskSpace == nil {
		diskSpace = cache.DiskSpace
	}
	free, total, err := diskSpace(d.root)
	if err != nil || d.minFreeGiB < 0 {
		return free, 0, err
	}
	if d.minFreeGiB > 0 {
		return free, uint64(d.minFreeGiB) * gib, nil
	}
	floor = min(total/autoFloorDivide, autoFloorMax)
	return free, max(floor, autoFloorMin), nil
}

// transcodeRoom: the cache's disk has its floor free for a new transcode. A
// disk whose free space cannot be read does not stop one.
func transcodeRoom(disk vodDisk, logger *slog.Logger) error {
	free, floor, err := disk.space()
	if err != nil {
		logger.Warn("vod: reading the cache disk's free space failed", "err", err.Error(), "event", "vod_disk_free_unknown")
		return nil
	}
	if free < floor {
		logger.Warn("vod: too little free on the cache's disk to start a transcode", "free_bytes", free,
			"min_free_bytes", floor, "event", "vod_disk_low")
		return fmt.Errorf("vod: %d bytes free on the cache's disk, under the %d floor", free, floor)
	}
	return nil
}

// lastWatched is when the session's player last fetched something (its close,
// when that came later).
func lastWatched(row *session.Row) time.Time {
	if row.ClosedAt != nil && row.ClosedAt.After(row.LastSegmentAt) {
		return *row.ClosedAt
	}
	return row.LastSegmentAt
}

// purgeVODSessions removes the VOD sessions' folders this process no longer
// serves (their playlists list every segment, so a folder grows with its
// transcode):
//
//   - a session's closed by its player (or an eviction) at once: its ffmpeg was
//     stopped as it closed;
//   - one closed for being idle, vodFinishedGrace after its last fetch when its
//     transcode was finished, else vodSessionGrace: its player may come back;
//   - one no session here has (a previous run's: the sessions are in memory)
//     once it has not changed for vodSessionIdle (a session being opened writes
//     its folder before its row is stored);
//   - and with less than the floor free on the disk, those it keeps for closed
//     sessions, the longest unwatched first, until the floor is free.
//
// An open session's stays.
func purgeVODSessions(ctx context.Context, layout *cache.Layout, store session.Store, disk vodDisk, now time.Time, logger *slog.Logger) {
	ids, err := layout.SessionDirs()
	if err != nil {
		logger.Warn("vod: listing the session folders failed", "err", err.Error(), "event", "vod_session_purge_failed")
		return
	}
	purge := func(name, reason string) bool {
		if err := layout.PurgeSession(name); err != nil {
			// a file still open (Windows): the next sweep tries again
			logger.Warn("vod: removing a session folder failed", "session_id", name, "reason", reason,
				"err", err.Error(), "event", "vod_session_purge_failed")
			return false
		}
		logger.Info("vod: session folder removed", "session_id", name, "reason", reason, "event", "vod_session_purged")
		return true
	}
	type keptFolder struct {
		name    string
		watched time.Time
	}
	var kept []keptFolder // closed sessions' folders kept for now
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
			grace := vodSessionGrace
			if row.TranscodeFinished() {
				grace = vodFinishedGrace
			}
			if watched := lastWatched(row); now.Sub(watched) < grace {
				kept = append(kept, keptFolder{name, watched})
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
		purge(name, reason)
	}
	if len(kept) == 0 {
		return
	}
	free, floor, err := disk.space()
	if err != nil || free >= floor {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].watched.Before(kept[j].watched) })
	for _, k := range kept {
		if !purge(k.name, "disk_low") {
			continue
		}
		if free, floor, err = disk.space(); err != nil || free >= floor {
			return
		}
	}
}
