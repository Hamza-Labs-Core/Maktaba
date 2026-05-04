# Implementation Plan — Story 11.3 Video Player

> Companion to [story-11-03-video-player.md](story-11-03-video-player.md).
> Player implementation; depends on Epic 7 Stories 7.10 (sessions), 7.11
> (watch progress) and Epic 8 Stories 8.5 (HLS), 8.11 (subtitles), 8.12
> (chapters).
> Session handshake is canonical per REVIEW §4.1 (resolved): always
> `POST /api/stream/sessions` first.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | Vidstack (`@vidstack/react`) wrapping HLS.js for non-native-HLS browsers. |
| Placement | `web/src/features/player/` — `Player.tsx`, `useStreamSession.ts`, `useWatchProgress.ts`, `usePlayerHotkeys.ts`. |
| Session minting | `useStreamSession({ videoId })` calls `POST /api/stream/sessions`; refetches on `expires_at - 30s`. |
| URL choice | If `mode === 'direct' && direct_url`, use `direct_url` (audience `streaming-direct`). Else use `manifest_url` (audience `streaming`). The web client never calls `GET /stream/direct/{video_id}` — that endpoint is reserved for native players (Story 12.3). |
| Watch progress | POST every 10 s and on pause/seek. No monotonicity check (REVIEW §1.5.a); no `seek=true` flag. |
| Out of scope | Native player handoff (Story 12.3); cast/AirPlay (Story 12.7). |

## 1. Component & hook layout

```
<Player videoId startSec? autoPlay?>
  ├─ useStreamSession(videoId)
  ├─ useWatchProgress(sessionId, videoId)
  ├─ usePlayerHotkeys(playerRef)
  ├─ <vidstack.MediaPlayer src={chosenUrl} ...>
  │     ├─ <Poster/>
  │     ├─ <ChapterMarkers chapters={chapters}/>     (overlays scrubber)
  │     ├─ <SubtitleOverlay tracks={subtitleTracks}/> (RTL-correct)
  │     └─ <SpeedControl/>
  └─ <PiPButton/>  <FullscreenButton/>
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/features/player/Player.tsx` | Public component; wraps Vidstack + hooks. |
| `web/src/features/player/useStreamSession.ts` | Session lifecycle (open, refresh, close, change-track). |
| `web/src/features/player/useWatchProgress.ts` | 10 s ticker + pause/seek listeners → `POST .../progress`. |
| `web/src/features/player/usePlayerHotkeys.ts` | Space/J/K/L/M/0–9/F/C/+/- and chapter `.`/`,`. |
| `web/src/features/player/useChapters.ts` | Fetch `chapters.json` once per video (from `GET /api/videos/{id}/chapters`). |
| `web/src/features/player/useSubtitles.ts` | Fetch tracks; live-swap when default changes. |
| `web/src/features/player/components/SubtitleOverlay.tsx` | CSS-styled overlay; respects user style settings. |
| `web/src/features/player/components/ChapterMarkers.tsx` | Renders ticks on `<media-time-slider>`. |
| `web/src/features/player/components/PlayerStats.tsx` | Optional overlay (bitrate, resolution, dropped). |
| `web/src/features/player/types.ts` | `StreamSession`, `Chapter`, `SubtitleTrack`, `AudioTrack` types. |

## 3. Session handshake

```ts
// useStreamSession.ts
async function openSession(videoId: string): Promise<StreamSession> {
  const res = await api.post('/stream/sessions', {
    video_id: videoId,
    capabilities: detectCodecCapabilities(),
  }, { headers: { 'Idempotency-Key': uuidv4() } });
  return res.data;          // { session_id, mode, manifest_url, direct_url?, expires_at, ladder, current_rendition }
}

const chosenUrl = session?.mode === 'direct' && session.direct_url
  ? session.direct_url
  : session.manifest_url;
```

Refresh schedule:
- Set a `setTimeout` for `(expires_at - now()) - 30_000`.
- On fire, re-open the session; pass current `last_offset_sec` so the server can resume cleanly.
- On manifest 401/403 mid-watch, re-open immediately and seek to current position.

Session close:
- On unmount or videoId change: `DELETE /api/stream/sessions/{session_id}`.
- On audio-track change: close and re-open with new `audio_track_index`.

## 4. Watch progress

```ts
useEffect(() => {
  const interval = setInterval(() => post(currentTime), 10_000);
  player.addEventListener('pause', () => post(currentTime));
  player.addEventListener('seeked', () => post(currentTime));
  return () => clearInterval(interval);
}, [sessionId]);

async function post(timeSec: number) {
  await api.post(`/stream/sessions/${sessionId}/progress`,
    { offset_sec: timeSec });
}
```

No monotonicity check, no `seek` flag (REVIEW §1.5.a is resolved in favor of last-writer-wins).

## 5. Hotkeys

| Key | Action |
|---|---|
| `Space`, `K` | Toggle play |
| `←` / `→` | ±10 s |
| `Shift+←` / `Shift+→` | ±30 s |
| `J` / `L` | ±10 s |
| `M` | Mute toggle |
| `,` / `.` | Previous / next chapter |
| `0`–`9` | Jump to N×10% |
| `F` | Fullscreen |
| `C` | Toggle subtitles |
| `+` / `-` | Speed step |

In RTL mode with "use logical arrows" enabled, `→` becomes "back" and `←` becomes "forward" (logical scrubbing). Respects Story 11.9 hotkey gating (no fire when an input is focused).

## 6. Subtitles

- Tracks loaded as `<track src="…" kind="subtitles" srclang="…" label="…">`.
- Style controls applied via CSS variables on the overlay container; `.cue { font-size: var(--cue-font-size); … }`.
- RTL: cues render with `direction: rtl` when `srclang` matches an RTL locale; mixed-direction cues use Unicode bidi isolates already in the VTT.
- Default track: `useSubtitles` reads `playbackState.preferredSubtitleLang` and selects the matching track on load.
- Track switch in mid-playback: no playback hiccup — Vidstack swaps via `track.mode = 'showing'`.

## 7. Chapters

- Markers drawn as `<button>` ticks layered on the time slider; each carries `aria-label` with chapter title and time. Hover surfaces a tooltip; click `seek(start_sec)`.
- Source: `chapters.json` produced by Epic 1 `chapter_infer` stage (REVIEW §2.7.a is resolved by adding that stage).

## 8. Picture-in-picture

- Dedicated PiP button uses `requestPictureInPicture()`. On supported browsers the player survives navigation away from `/watch/{id}` because PiP is owned by the browser.
- Persistence across React Router transitions: open question in epic README; default for v1 is "always re-create with saved position".

## 9. Edge cases

| Case | Handling |
|---|---|
| Manifest expires mid-watch | Refresh transparently; gap < 1 s; preserve position. |
| Audio track change | Close + reopen session; UI shows brief "Switching track…". |
| Autoplay blocked | Start muted with "Click to unmute" overlay. |
| HLS.js bootstrap fails | Re-mint session with `force_transcode=false`; if still broken, surface error + Retry. |
| Manifest disagrees with metadata duration | Trust manifest's `EXT-X-ENDLIST`. |
| 8 s network drop | HLS.js retries; we show spinner overlay; no manual intervention. |

## 10. Test cases

### 10.1 Unit (Vitest)

| Test | Asserts |
|---|---|
| `chooses direct_url for mode=direct` | URL passed to MediaPlayer matches `direct_url`. |
| `chooses manifest_url for mode=transcode` | URL passed to MediaPlayer matches `manifest_url`. |
| `posts progress every 10s and on pause` | Mock interval; verify call counts and payloads. |
| `seek does not include seek flag` | Body shape: `{ offset_sec }` only. |
| `audio track change closes session` | DELETE called before next session POST. |

### 10.2 e2e (Playwright)

| Test | Asserts |
|---|---|
| `direct play H.264+AAC in Chrome` | `mode = direct`; player joins ≤ 2 s. |
| `transcode play HEVC in Safari` | `mode = transcode`; HLS manifest loads. |
| `seek 35 min on 1 h video` | Catches up ≤ 3 s. |
| `subtitles toggle without hiccup` | No buffering pause between toggles. |
| `multi-tab resume` | Tab B opens video; "Resume at 35:14" offered. |
| `8 s network drop recovers` | After offline window, playback resumes within 5 s. |
| `backward scrub accepted` | Server accepts decreasing `offset_sec`. |
| `manifest expires mid-watch` | Captured via fixture with short `expires_at`; refresh succeeds; gap ≤ 1 s. |

## 11. Performance

- Player join (cold) ≤ 2 s on 100 Mbps LAN — measured via `player-loaded` event timestamp.
- Subtitles toggle ≤ 50 ms.
- Chapter marker render ≤ 16 ms even at 200 chapters.
- Bundle: HLS.js loaded only when needed (dynamic import on non-Safari).

## 12. Dependencies

- API: Epic 7 Stories 7.10, 7.11; Epic 8 Stories 8.5, 8.11, 8.12.
- Auth: refresh tokens in cookies (Epic 10 Story 10.3).
- Bundles into Stories 12.3 (native player echoes events here) and 12.5 (background playback uses the same hooks).
