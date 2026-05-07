# Story 14.5 — Continue Watching row

The Home screen's first row, populated from `playback_state` with
in-progress videos sorted by most recently played.

**Anchors:** [`architecture.md` §6.5](../../architecture.md). Depends on
Epic 7 Story 7.11 (watch progress).

## Index requirement (resolves [REVIEW §6.3](../../REVIEW.md))

The cross-device "appears within 5 s of the last position update"
guarantee depends on a covering index over `playback_state`:

```sql
CREATE INDEX playback_state_user_updated_idx
  ON playback_state (user_id, updated_at DESC)
  WHERE position_sec >= duration_sec * 0.05
    AND position_sec <  duration_sec * 0.95;
```

The index is partial so it only contains the eligible "in-progress"
rows (5%–95% progress). The migration is owned by **this story** since
the row is exclusively a TV-UX requirement; Epic 7 Story 7.11 only
needed the table itself.

## AC

- Row title: "Continue Watching" (or localized).
- Items: poster + title + remaining time + progress bar overlay.
- Min progress to qualify: 5%; max progress: 95% (above that the video
  is "Watched").
- Cross-device: started on phone shows up on TV within 5 s of the last
  position update (relies on the partial index above; the WS
  `playback.changed` event from Epic 7 Story 7.16 also notifies live
  clients).
- Long-press / context menu: "Mark as Watched", "Remove from Continue".
- Empty state: "Nothing in progress yet — start a video on any device".

## TC

- Watch 12 minutes of a 1-hour video on the phone: the row updates on
  the TV within 5 s.
- Mark watched on phone: row entry disappears on TV.
- Delete the underlying video: row entry hidden.
- Run `EXPLAIN` on the row's query at 100k `playback_state` rows: the
  planner uses `playback_state_user_updated_idx`, no seq scan.

## EC

- A video with `duration_sec = 0` (probe pending): excluded from the
  row.
- A user with > 50 in-progress videos: row caps at 20, sorted by
  recency.
- Duplicate entries (same video in two collections): single entry only.
