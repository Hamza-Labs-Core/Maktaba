# Epic 01 — Scanner

**Goal.** Detect every video file under a library's roots, assign it a
stable identity (`content_hash`), and create a `videos` row in state
`DISCOVERED`. Cope with renames, moves, copies, partial downloads, network
filesystems that lie about events, and the user dragging in a 200 GB folder
in one go.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/library/`.

**Out of scope for this epic.** Probing (Epic 2 prerequisite handled by the
`probe` stage in §3.2 of the architecture), subtitles auto-discovery
(Epic 4), thumbnails.

## Stories

| # | Title | File |
|---|-------|------|
| 1.1 | Bootstrap a library and walk its roots | [story-01-01-file-discovery.md](story-01-01-file-discovery.md) |
| 1.2 | Content-addressable identity (BLAKE3) | [story-01-02-content-identity.md](story-01-02-content-identity.md) |
| 1.3 | Watch for live filesystem changes | [story-01-03-filesystem-watcher.md](story-01-03-filesystem-watcher.md) |
| 1.4 | Manual control surface | [story-01-04-manual-control.md](story-01-04-manual-control.md) |
| 1.5 | Schema & ownership decisions | [story-01-05-schema-decisions.md](story-01-05-schema-decisions.md) |
| 1.6 | Video state machine | [story-01-06-video-state-machine.md](story-01-06-video-state-machine.md) |

## Dependency notes

- 1.1 unblocks every other Scanner story.
- 1.6 (state machine) is a contract referenced by Epics 2, 3, 4, 5, and 9.
  All transitions out of `DISCOVERED` are driven by downstream epics; this
  epic owns the schema and the explicit FSM.
- The whole epic depends on Epic 6 (Job Queue) Stories 6.1–6.3 for
  `processing_jobs` enqueue and observability.
