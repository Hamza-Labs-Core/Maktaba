# Story 54.33 — Wave 6: YouTube and repair

> Epic 54 · Phase 4 · Depends on: 54.32 · Needed by: 54.34

## Why

These stages reach the internet (YouTube through `yt-dlp` and the YouTube API) and write
downloaded media into libraries. They move into the `net` runner, the only runner group whose
sandbox allows outbound network, with their hosts declared and checked in CI.

## Description

### 1. The stages

| Stage | Plugin | Pool | Placement | Trusted needs |
|---|---|---|---|---|
| `youtube_sync` | `tv.maktaba.youtube` | `network_yt` | any | `net` (YouTube hosts), `exec:yt-dlp` |
| `youtube_fetch` | `tv.maktaba.youtube` | `network_yt` | any | `net`, `exec:yt-dlp`, `files.write:library` |
| `youtube_backfill` | `tv.maktaba.youtube` | `network_yt` | any | same |
| `exact_dates` | `tv.maktaba.youtube` | `network_yt` | any | `net`, `exec:yt-dlp` |
| `repair_broken` | `tv.maktaba.repair` | `repairs` | any | `net`, `exec:yt-dlp`, `files.write:library` |

Hosts: `www.youtube.com`, `youtube.com`, `*.googlevideo.com`, `www.googleapis.com`,
`i.ytimg.com` (the list is made from what `integrations/youtube.py`,
`integrations/youtube_download.py` and `yt-dlp`'s YouTube extractor contact in the parity
recording run, and reviewed in the PR).

### 2. yt-dlp

`yt-dlp` is the Epic 51 asset `tool.yt-dlp`; the runner's `exec` allows only that binary. The
fake used by parity is a script on `PATH` that serves recorded listings and media.

### 3. The work

As wave 1. `_youtube_drain.py` and the downloads' Epic 47 priorities move into the plugin.
The YouTube API routes in the Go API (`/api/libraries/{id}/youtube/*`, sources, Tartube import)
stay and keep enqueueing the same stage names.

## Acceptance criteria

- **Given** the parity fixture with the recorded YouTube fake,
  **when** `plugins-parity` runs the five stages,
  **then** rows, downloaded files, results, fingerprints and enqueued jobs are identical, and
  N16 holds.
- **Given** the CI recording proxy,
  **when** the stages run against the real `yt-dlp` with the network recorded (nightly only,
  one public test video),
  **then** only declared hosts are contacted.
- **Given** the `net` runner,
  **when** a YouTube stage asks for a path outside its library,
  **then** the API refuses it.
- **Given** an update mid-download and a rollback,
  **when** they finish,
  **then** the download resumes or restarts as today, with no duplicate file.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | recorded fake | five stages | identical; N16 |
| T02 | nightly (CI only) | real `yt-dlp`, recording proxy, one public test video | sync and fetch | declared hosts only |
| T03 | integration (py) | `media_path` outside the library | call | refused |
| T04 | update rehearsal scenario 16.6 | mid-download | update and rollback | no duplicate, no loss |
| T05 | unit (`fp-youtube`, `fp-repair`) | moved tests | run | green |

## Edge cases

- **YouTube changes and `yt-dlp` needs an update**: unchanged, a server release with a new
  `tool.yt-dlp` pack (first-party packs move with the server version, Epic 51 51.15).
- **A host YouTube adds** (a new CDN name): the nightly T02 fails, and the fix is a reviewed
  manifest edit.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/youtube/`, `plugins/first-party/repair/` (new) | §1–§3; `integrations/youtube*.py` move. |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/` | Remove moved definitions after one release. |
| `shared/db/migrations/` | Roles; `impl = plugin` for the five. |
| `tests/update_rehearsal/scenario_16_6_wave6.*` | T04. |

### Tests

- Parity T01; nightly T02; integration T03; rehearsal T04; unit T05.

### Rollout

The release flips the five at update.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.32.
