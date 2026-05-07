# Implementation Plan — Story 11.2 Video Detail Page

> Companion to [story-11-02-video-detail-page.md](story-11-02-video-detail-page.md).
> The story states *what* and *why*; this plan states *how*.
> Player itself is owned by [Story 11.3](story-11-03-video-player.md);
> data shapes anchored by Epic 7 Stories 7.4 (read), 7.5 (process control),
> 7.6 (transcript window), 7.7 (subtitles), 7.16 (WS).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Route | `/watch/:id` with optional `?t=<seconds>&tab=<watch|transcript|chapters|files|processing>`. |
| Placement | `web/src/routes/watch/[id].tsx`, `web/src/features/watch/`. |
| Data | Single GraphQL query `videoDetail($id)` with fragments per tab; subscriptions over `lib/ws.ts` for `/ws/jobs` and `/ws/library/{id}`. |
| Player | Renders `<Player>` from Story 11.3; this story owns only the chrome and tabs. |
| Out of scope | Manifest/session minting (11.3); search (11.4); queue page (11.5). |

## 1. Component tree

```
<WatchPage videoId>
 ├─ <WatchHeader>              poster, title, lang flags, type, library, file path (admin)
 ├─ <Tabs>
 │    Watch | Transcript | Chapters | Files | Processing
 ├─ <WatchTab>
 │    <Player/>                (Story 11.3)
 │    <ResumeBanner/>          shown when playback_state has offset > 0
 ├─ <TranscriptTab>
 │    <TranscriptList/>        TanStack Virtual; live-updates from WS
 │    <TranscriptControls/>    follow-playhead toggle, language picker
 ├─ <ChaptersTab>
 │    <ChapterList/>           click jumps to chapter.start_sec
 ├─ <FilesTab>
 │    <SubtitleTrackList/>     source/lang/format + "set as default"
 │    <AudioTrackList/>        codec/channels/lang + "play this track"
 └─ <ProcessingTab>
      <JobRow/>                per processing_jobs row; pause/resume/cancel/retry/move-priority
      <ReprocessFromStage/>    confirmation modal
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/routes/watch/[id].tsx` | Lazy route. |
| `web/src/features/watch/WatchPage.tsx` | Top-level layout + tab routing via `?tab=`. |
| `web/src/features/watch/useVideoDetail.ts` | TanStack Query over GraphQL; merges initial fetch with WS deltas. |
| `web/src/features/watch/useJobsForVideo.ts` | `GET /api/videos/{id}/jobs` + `/ws/jobs` filtered by `videoId`. |
| `web/src/features/watch/components/WatchHeader.tsx` | Poster, title, badges, file path (gated). |
| `web/src/features/watch/components/Tabs.tsx` | `?tab=` synced; arrow-key cycling. |
| `web/src/features/watch/components/TranscriptList.tsx` | Virtualized; click-to-seek; highlights current segment. |
| `web/src/features/watch/components/ChapterList.tsx` | List rows + scrubber tick handoff to player. |
| `web/src/features/watch/components/SubtitleTrackList.tsx` | + "set as default" → `PATCH playback_state.preferred_subtitle_lang`. |
| `web/src/features/watch/components/AudioTrackList.tsx` | "Play this track" closes session and reopens (player coordinates). |
| `web/src/features/watch/components/JobRow.tsx` | Stage badge, state badge, progress, ETA, actions. |
| `web/src/features/watch/components/ReprocessModal.tsx` | Confirm + dispatch `POST /api/videos/{id}/reprocess` with stage. |
| `web/src/lib/ws.ts` | Centralized WS client: **one socket per channel** (`/ws/jobs`, `/ws/library/{id}`, `/ws/segments/{video_id}`); topics fanned-in via the `useWsTopic` hook. The server exposes three separate `/ws/*` routes, so the client maintains one socket per route, not one global socket. |
| `shared/graphql/queries/videoDetail.graphql` | Query + fragments. |

## 3. Data model

```ts
type VideoDetail = {
  id: string; title: string; durationSec: number;
  posterUrl: string; library: { id: string; name: string };
  filePath?: string;       // admin-only
  state: 'PROCESSING'|'READY'|'FAILED'|'MISSING'|'READY_NO_AUDIO'|'SUPERSEDED'|'CORRUPTED';
  langs: string[]; type: string;
  jobs: Job[];
  subtitleTracks: SubtitleTrack[];
  audioTracks: AudioTrack[];
  chapters: Chapter[];
  playbackState?: { offsetSec: number; preferredSubtitleLang?: string };
  supersededBy?: string;   // when state = SUPERSEDED → redirect target
};

type Job = {
  id: string;
  stage: 'scan'|'probe'|'extract'|'transcribe'|'subtitle_gen'|'index'|'thumbnail';
  state: 'pending'|'running'|'paused'|'done'|'failed'|'cancelled';
  processedSec: number; totalSec: number;
  attempts: number; etaSec?: number;
  error?: string; reapedReason?: string;
  pauseRequested: boolean; lastHeartbeatAt?: string;
};
```

Stage names match REVIEW §1.3.b/c (note `thumbnail` not `thumb`; `subtitle_gen` is a real stage).

## 4. Implementation steps

### 4.1 Query + WS merge

```ts
const detail = useQuery(['video', id], fetchVideoDetail);
useWsTopic(`/ws/library/${detail.data?.library.id}`, (msg) => {
  if (msg.video_id === id) qc.invalidateQueries(['video', id]);
});
useWsTopic(`/ws/jobs`, (msg) => {
  if (msg.video_id === id) qc.setQueryData(['video', id], applyJobUpdate(msg));
});
```

`applyJobUpdate` mutates only the matching `jobs[i]` row (immer); avoid full refetch.

### 4.2 Tabs and URL state

Active tab is `?tab=` (default `watch`). Tab order: Watch → Transcript → Chapters → Files → Processing. Arrow-key cycling on the tablist (`role="tablist"`, ARIA managed by Story 17.2 primitive).

### 4.3 Transcript tab

- Source: `GET /api/videos/{id}/segments?from=&to=` (Epic 7 Story 7.6, architecture §9 canonical). Store as `Map<segmentId, Segment>`. React Query key: `['video', id, 'segments', { from, to }]`.
- WS deltas: on `segment.committed` events for this video, prepend to the map and reorder via `start_sec`.
- Click on segment row → seek the player via `playerApi.seekTo(start_sec)` (the player exposes a stable ref).
- Follow-playhead toggle: when on, the list scrolls so the current segment is centered (smooth, throttled to 4 Hz).

### 4.4 Processing tab

Each `<JobRow>` exposes Pause / Resume / Cancel / Retry / Move-to-front.
Actions hit `POST /api/jobs/{id}/{action}` (Epic 7 Story 7.12). Optimistic update sets the row's state immediately; reconciles on WS or 5 s timeout.

The "Reprocess from `transcribe`" affordance opens `<ReprocessModal>`:

```ts
await fetch(`/api/videos/${id}/reprocess`, {
  method: 'POST', body: JSON.stringify({ from_stage: 'transcribe' }),
  headers: { 'Idempotency-Key': uuidv4() },
});
```

### 4.5 Subtitle / audio tracks

"Set as default" → `PATCH /api/me/playback-state { videoId, preferredSubtitleLang }`. Emits an event the player picks up to swap tracks live.

"Play this track" closes the active session via `DELETE /api/stream/sessions/{id}` and re-opens with the new audio selection (Story 11.3 handles the open).

### 4.6 Edge cases

| Case | Handling |
|---|---|
| `state = MISSING` / `CORRUPTED` | Player surface replaced by `<UnplayableNotice>`; Processing tab surfaces "Rescan" CTA. |
| `state = SUPERSEDED` | `useEffect` redirects to `supersededBy` with a sticky banner "Redirected to active twin". |
| Failed job with long `error` | `<JobRow>` clamps to 2 lines; "Show details" toggles a `<dialog>` with copy-to-clipboard. |
| Subtitle file missing on disk | Row greyed; tooltip "File missing — rescan needed". |
| > 50k transcript segments | Virtualized; first paint ≤ 500 ms verified by Playwright trace. |
| Reaped job | Banner inside `<JobRow>` with `reapedReason`. |

## 5. Test cases

### 5.1 Unit

| Test | Asserts |
|---|---|
| `live transcribe at 23% renders partial transcript` | Initial fixture has 23% segments; mock WS pushes 5 more; list grows to N+5. |
| `subtitle switch updates default` | Mock `PATCH /api/me/playback-state`; player receives event; UI shows new "Default" badge. |
| `reprocess modal blocks until confirm` | Submit button disabled until user types stage name. |
| `non-admin cannot see file path` | `useFeatureFlag('admin') === false`; element absent. |
| `superseded redirects` | Renders empty page, navigates to `supersededBy` route. |

### 5.2 e2e

| Test | Asserts |
|---|---|
| `pause + resume from Processing tab` | Confirms job transitions paused → running with no UI flicker. |
| `transcript follow-playhead centers row` | Player seeks; transcript scrolls; current segment has `aria-current`. |
| `failed job error truncation` | Long error truncates; "Show details" reveals full text + copy-to-clipboard. |

## 6. Performance

- TanStack Virtual + windowing keeps DOM nodes ≤ 60 even at 50k segments.
- Apply WS updates with `setQueryData` (no refetch). Throttle to 5 Hz per `videoId`.
- Initial GraphQL query is a single round-trip; tab-specific fragments are deferred via `@defer` where supported.

## 7. Dependencies

- Player: Story 11.3.
- WS plumbing: shared `lib/ws.ts` (used by Stories 11.5 and 11.10 too).
- API surface: Epic 7 Stories 7.4, 7.5, 7.6, 7.7, 7.12, 7.16.
