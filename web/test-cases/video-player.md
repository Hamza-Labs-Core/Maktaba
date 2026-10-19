# Video player

- **Route:** `/videos/:videoId/watch`, optional `?t=<seconds>` start position (set by transcript, chapter and search-hit links).
- **Component:** `src/pages/VideoPlayer.tsx`; uses `src/lib/watchSession.ts` (`createWatchSession`), `@ds` `Button`, `ErrorState`. No children of its own; native `<video controls>` (`data-testid="mkt-video"`).
- **API calls:**
  - `POST /api/stream/sessions { video_id }` via `api.post` → `{ session_id, mode, manifest_url?, direct_url?, expires_at }`. `src` = `manifest_url || direct_url || ""`. The URLs are signed by the server (commit `577b1229`), so they carry signature query params that must be used as-is.
  - `POST /api/stream/sessions/{session_id}/progress { position_sec[, completed: true] }` on `timeupdate` (at most every 5 s), `pause`, `ended`.
  - `DELETE /api/stream/sessions/{session_id}` on unmount and on `videoId` change.
  - `GET /api/videos/{id}/chapters` → `{ items: [{ id, start_sec, title }] }` (scrubber ticks, shown only when `duration > 0`).
  - Watch analytics (`watchSession.ts` → `analyticsApi` in `src/lib/analytics.ts`): `POST /api/watch/start { video_id, device_type, platform, quality }` → `{ tracking, session_id? }` on `play`; `POST /api/watch/heartbeat { session_id, position_sec }` every 30 s; `POST /api/watch/stop { session_id, position_sec }` on pause/ended/unmount; keepalive `POST /api/watch/stop` on `pagehide`.
- **Keyboard (on the focused `section.mkt-player`):** Space/`k` play-pause, `j`/`l` -/+10 s, ArrowLeft/ArrowRight -/+5 s, `m` mute, `,`/`.` frame step, `0`–`9` seek to N0 %, `f` fullscreen, `c` toggle first text track, `+`/`=`/`-` speed. Global shortcuts (`src/lib/keyboard/shortcuts.tsx`): `/`, `?`, `g` + letter.
- **Subtitles / transcripts:** the player renders no `<track>` elements, so `c` and "Subtitle size" have nothing to act on today.
- **Resume:** only `?t=`. The page does not read a saved position from the server.
- **Access:** signed-in user (`RequireAuth`); server decides if the user may stream the video (ACL) on `POST /api/stream/sessions`.
- **Existing tests:** `src/pages/VideoPlayer.test.tsx` — covers: POST session with `video_id`, plays `manifest_url`; uses `direct_url` in direct mode; posts progress with `position_sec`; DELETE on unmount; DELETE of the prior session on `videoId` change; no DELETE when no session opened. `src/lib/watchSession.test.ts` — covers start with client info, 30 s beats, start no-op while open + reopen after stop, stop, tracking paused, pagehide beacon, start rejection swallowed. Gaps: errors, `?t=`, keyboard, speed/caption controls, chapters ticks, playback errors, signed URL handling, the player wiring of the watch session, crash fallback, RTL.

## Summary

| ID     | Title                                                      | Category      | Priority | Covered by existing test?                                               |
| ------ | ---------------------------------------------------------- | ------------- | -------- | ----------------------------------------------------------------------- |
| VPL-01 | Opens a stream session and plays `manifest_url`            | Happy path    | P1       | Yes (`VideoPlayer.test.tsx`)                                            |
| VPL-02 | Falls back to `direct_url`                                 | Happy path    | P1       | Yes (`VideoPlayer.test.tsx`)                                            |
| VPL-03 | Signed URL is used exactly as returned                     | Happy path    | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-04 | HLS manifest on a browser without native HLS               | Error         | P1       | Yes (`VideoPlayer.hls.test.tsx`)                                        |
| VPL-05 | Session with no URL at all                                 | Error         | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-06 | Playback error (expired signature / 403 / decode) is shown | Error         | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-07 | Session open 403 / 404 (no ACL, unknown id)                | Permissions   | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-08 | Signed-out user is sent to `/login`                        | Permissions   | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-09 | `?t=` start position; bad values ignored                   | Navigation    | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-10 | Progress posts: throttled, on pause, completed on end      | Happy path    | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-11 | Streaming session closed on unmount / video change         | Happy path    | P1       | Yes (`VideoPlayer.test.tsx`, `VideoPlayer.cases.test.tsx`)              |
| VPL-12 | Leaving before the session opens does not leak a session   | Happy path    | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-13 | Watch analytics: start on play, stop on pause/end/unmount  | Happy path    | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-14 | Analytics tracking paused or failing never breaks playback | Error         | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-15 | Chapters `items: null` / missing and ticks placement       | Empty state   | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-16 | Render crash shows route error fallback; Back still works  | Error         | P1       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-17 | Loading text while the session opens                       | Loading       | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-18 | Session open 500 / network / malformed JSON                | Error         | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-19 | Error clears when moving to another video                  | Navigation    | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-20 | Back button: in-app history and direct deep link           | Navigation    | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-21 | Keyboard map works on the focused player                   | Accessibility | P2       | Yes (`VideoPlayer.cases.test.tsx`; it.todo: visible shortcut list)      |
| VPL-22 | Shortcuts do not fire from form controls or with modifiers | Accessibility | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-23 | Speed select and +/- keys stay in sync                     | Happy path    | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-24 | Subtitles: tracks loaded, `c` toggles, size select applies | Happy path    | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-25 | Picture in picture toggle, unsupported is non-fatal        | Happy path    | P3       | No                                                                      |
| VPL-26 | Session expiry (`expires_at`) during long playback         | Error         | P2       | Yes (`VideoPlayer.cases.test.tsx`)                                      |
| VPL-27 | RTL: Back arrow, chapter ticks vs. native scrubber         | RTL           | P2       | Yes (`VideoPlayer.cases.test.tsx`; e2e-only: ticks vs. native scrubber) |
| VPL-28 | Phone width: controls wrap, video fits                     | Responsive    | P3       | No                                                                      |
| VPL-29 | Light/dark theme: control bar readable                     | Theme         | P3       | No                                                                      |

## Cases

### VPL-01 — Opens a stream session and plays `manifest_url`

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `VideoPlayer.test.tsx` "opens a stream session via POST /api/stream/sessions and plays manifest_url".
- **Preconditions / data:** `POST /api/stream/sessions` → `{ "session_id": "sess-abc", "mode": "transcode", "manifest_url": "/stream/sess-abc/manifest.m3u8", "direct_url": "", "expires_at": "2026-10-19T12:00:00Z" }`; chapters → `{ "items": [] }`.
- **Steps:**
  1. Open `/videos/vid-1/watch`.
- **Expected:** POST body `{ video_id: "vid-1" }`; `<video data-testid="mkt-video" src="/stream/sess-abc/manifest.m3u8" controls playsinline preload="metadata">`; no request to `/api/videos/vid-1/stream`. Control bar shows "← Back", "Playback speed", "Subtitle size", "Picture in picture".

### VPL-02 — Falls back to `direct_url`

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `VideoPlayer.test.tsx` "uses direct_url when the server picks direct mode".
- **Preconditions / data:** session → `{ "mode": "direct", "manifest_url": "", "direct_url": "/files/original.mp4", ... }`; also run with `manifest_url` missing and `null`.
- **Steps:**
  1. Open `/videos/vid-1/watch`.
- **Expected:** `src="/files/original.mp4"` in all three runs.

### VPL-03 — Signed URL is used exactly as returned

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** session → `{ "session_id": "s1", "mode": "direct", "direct_url": "/api/stream/s1/file?exp=1760870400&sig=a%2Bb%2F%3D", "manifest_url": "" }`. E2e variant: real API, real video.
- **Steps:**
  1. Open `/videos/vid-1/watch`.
- **Expected:** the `src` attribute equals the returned string byte for byte (no decoding of `%2B`/`%2F`, no dropped query). E2e: the media request returns 200/206, not 403; segment requests from the manifest are also signed and succeed.

### VPL-04 — HLS manifest on a browser without native HLS

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.hls.test.tsx`)
- **Preconditions / data:** jsdom: `HTMLMediaElement.prototype.canPlayType = () => ""`; session → `{ "mode": "transcode", "manifest_url": "/stream/s/manifest.m3u8", "direct_url": "/files/o.mp4" }`. E2e: Chromium (no native HLS).
- **Steps:**
  1. Open `/videos/vid-1/watch`.
- **Expected:** wanted: when `canPlayType("application/vnd.apple.mpegurl")` is empty, the player uses `direct_url` (as the file header comment says), or shows a clear "can't play here" message when there is no direct URL. Today `manifest_url` is always preferred, so Chrome/Firefox get an m3u8 they cannot play and a black video — likely bug.

### VPL-05 — Session with no URL at all

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** session → `{ "session_id": "s1", "mode": "transcode", "manifest_url": "", "direct_url": "", "expires_at": "..." }`; also `{}` (malformed: no `session_id`).
- **Steps:**
  1. Open `/videos/vid-1/watch`.
- **Expected:** wanted: an ErrorState ("Something went wrong." or a playback-specific message), not a `<video src="">`. Today it renders an empty video (currently unhandled — likely bug). With `{}`, progress posts must not go to `/api/stream/sessions/undefined/progress`.

### VPL-06 — Playback error (expired signature / 403 / decode) is shown

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** VPL-01 session. In jsdom set `video.error = { code: 2 }` (MEDIA_ERR_NETWORK) and dispatch `error` on the video; also code 3 (decode) and 4 (src not supported). E2e: point `direct_url` at a URL that returns 403.
- **Steps:**
  1. Open `/videos/vid-1/watch`; trigger the error.
- **Expected:** wanted: a visible message in a `role="alert"` region with a "Try again" action that re-opens the session (new POST, new signed URL). Today there is no `error` listener — the user sees a dead player (currently unhandled — likely bug).

### VPL-07 — Session open 403 / 404 (no ACL, unknown id)

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** `POST /api/stream/sessions` → (a) 403 problem+json `{ "title": "forbidden", "status": 403 }`; (b) 404 `{ "title": "video not found", "status": 404 }`. E2e: non-admin without ACL on the library.
- **Steps:**
  1. Open `/videos/<id>/watch` directly.
- **Expected:** ErrorState "Something went wrong." with the problem title as description; no `<video>` rendered; no DELETE sent; no `/api/watch/start` sent. Wanted: distinct not-found / no-access wording and a way back (no Back button is rendered on the error state today).

### VPL-08 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** full `App`; `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/videos/vid-1/watch?t=120`.
- **Expected:** redirect to `/login`; no stream session opened. After sign-in the user returns to `/videos/vid-1/watch`; wanted: `?t=120` kept (today `RequireAuth` stores only the pathname — likely bug).

### VPL-09 — `?t=` start position; bad values ignored

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** VPL-01 session; set `video.duration = 600` and dispatch `loadedmetadata`.
- **Steps:**
  1. Open `/videos/vid-1/watch?t=125`, fire `loadedmetadata`.
  2. Repeat with `?t=abc`, `?t=-5`, `?t=0`, `?t=99999`, no `t`.
- **Expected:** step 1: `video.currentTime === 125`. `abc`, `-5`, `0`, none: currentTime not set (stays 0). `99999`: wanted: clamped to just before the end (today it is set to 99999 as-is). Wanted (not built): with no `?t=`, resume from the last saved position for this user (no API call for it exists — feature gap).

### VPL-10 — Progress posts: throttled, on pause, completed on end

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** VPL-01 session; fake timers / mock `Date.now`; set `currentTime` per step.
- **Steps:**
  1. Fire `timeupdate` at t=0 s (currentTime 10.7), again at +1 s, again at +6 s (currentTime 16.2).
  2. Fire `pause`.
  3. Fire `ended` (currentTime 600).
  4. Make the progress POST reject.
- **Expected:** step 1: two progress POSTs (`position_sec: 10`, then `16`), not three. Step 2: one POST with the current floored position. Step 3: POST `{ position_sec: 600, completed: true }`. Step 4: no uncaught rejection, playback UI unchanged.

### VPL-11 — Streaming session closed on unmount / video change

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.test.tsx`, `VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** as in the existing tests.
- **Steps:**
  1. Open, then unmount; separately navigate `/videos/vid-1/watch` → `/videos/vid-2/watch`.
- **Expected:** `DELETE /api/stream/sessions/sess-first` once; a new POST for `vid-2`; the video `src` switches to the new session's URL. Add: DELETE failure (500) is swallowed.

### VPL-12 — Leaving before the session opens does not leak a session

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** POST resolves 200 ms after mount with `{ "session_id": "late", ... }`.
- **Steps:**
  1. Open `/videos/vid-1/watch`; unmount after 50 ms; advance timers past 200 ms.
- **Expected:** wanted: `DELETE /api/stream/sessions/late` is sent once the late response arrives. Today the `cancelled` branch drops the id, so the server session leaks until it is reaped — likely bug. Same check when switching `videoId` before the first POST resolves.

### VPL-13 — Watch analytics: start on play, stop on pause/end/unmount

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** VPL-01 session (`mode: "transcode"`); `POST /api/watch/start` → `{ "tracking": true, "session_id": "w1" }`; fake timers.
- **Steps:**
  1. Fire `play` on the video.
  2. Advance 30 s.
  3. Fire `pause`; fire `play` again; fire `ended`.
  4. Fire `play`, then unmount.
  5. Fire `play`, then dispatch `pagehide` on `window`.
- **Expected:** step 1: `/api/watch/start` with `{ video_id: "vid-1", quality: "transcode", device_type, platform }`. Step 2: one `/api/watch/heartbeat { session_id: "w1", position_sec }`. Step 3: `/api/watch/stop`, then a second start, then stop. Step 4: stop on unmount. Step 5: keepalive fetch to `/api/watch/stop` with the CSRF header. Only one start per play even if `session.mode` re-renders. Also: the start happens only after the session `<video>` exists (the effect runs when `session?.mode` changes from undefined).

### VPL-14 — Analytics tracking paused or failing never breaks playback

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** (a) start → `{ "tracking": false }`; (b) start → 500; (c) heartbeat → network error.
- **Steps:**
  1. Fire `play`, advance 60 s, fire `pause`.
- **Expected:** (a) no heartbeat or stop sent; (b)(c) no uncaught rejection; video keeps playing; stream progress posts (VPL-10) still go out.

### VPL-15 — Chapters `items: null` / missing and ticks placement

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** chapters → (a) `{ "items": null }`, (b) `{}`, (c) 500, (d) `{ "items": [ { "id": 1, "start_sec": 0, "title": "Intro" }, { "id": 2, "start_sec": 300, "title": "Part 2" } ] }` with `duration = 600`. List field: `items`.
- **Steps:**
  1. Open the player; fire `loadedmetadata`.
- **Expected:** (a)–(c) no `.mkt-player__chapters` and no crash. (d) two ticks with `title` "Intro" / "Part 2" at `inset-inline-start: 0%` and `50%`; the tick strip is `aria-hidden`. Before `loadedmetadata` (duration 0) no ticks render. A chapter with `start_sec` > duration must not render outside the bar (wanted: clamp).

### VPL-16 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** full routes inside `AppShell`; start at `/videos/vid-1`; mock `@ds/components/Button/Button` (or `createWatchSession`) to throw while the player renders; or chapters → `{ "items": [ null ] }` (today `c.id` on `null` throws once duration > 0).
- **Steps:**
  1. Open `/videos/vid-1`; click "▶ Play".
  2. Press browser Back.
  3. Click the "Library" nav link.
- **Expected:** step 1 shows a route-level error fallback inside the shell, not a blank page; any opened stream session is still DELETEd. Steps 2 and 3 render their pages without a reload.

### VPL-17 — Loading text while the session opens

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** POST never resolves.
- **Steps:**
  1. Open the player.
- **Expected:** "Loading…" only; no `<video>`, no control bar. Wanted: the Back button is available while loading (today it is not rendered).

### VPL-18 — Session open 500 / network / malformed JSON

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** (a) 500 `{ "title": "transcoder busy", "status": 500 }`; (b) fetch rejects; (c) 200 JSON body `{bad`; (d) 503 `{ "code": "paused" }`.
- **Steps:**
  1. Open the player.
- **Expected:** (a) ErrorState "Something went wrong." / "transcoder busy"; (b)(c) description "Something went wrong."; (d) the NodeGuard paused page takes over (event `maktaba:node-unavailable`). Wanted: "Try again" on the ErrorState (none today).

### VPL-19 — Error clears when moving to another video

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** first POST (vid-1) → 403; second POST (vid-2) → valid session.
- **Steps:**
  1. Open `/videos/vid-1/watch`; wait for the error.
  2. Navigate in-app to `/videos/vid-2/watch`.
- **Expected:** wanted: the error goes away, "Loading…", then vid-2 plays. Today `err` is never reset in the effect, so the error stays for vid-2 — likely bug. Also: while vid-2's session opens, the old vid-1 `src` must not keep playing (today `session` is not reset either).

### VPL-20 — Back button: in-app history and direct deep link

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** full routes. (a) history `/videos/vid-1` → `/videos/vid-1/watch`; (b) open `/videos/vid-1/watch` as the first entry (e.g. pasted link).
- **Steps:**
  1. Click "← Back".
  2. Use the browser Back button from the player.
- **Expected:** (a) returns to `/videos/vid-1`; stream session DELETEd and watch session stopped. (b) wanted: goes to `/videos/vid-1` (or Library) instead of leaving the app; today `nav(-1)` with no history does nothing or leaves the site — likely bug.

### VPL-21 — Keyboard map works on the focused player

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`; it.todo: visible shortcut list)
- **Preconditions / data:** VPL-01 session; stub `video.play`, `video.pause`, `paused`, `duration = 600`, `currentTime = 100`, `requestFullscreen`, `document.exitFullscreen`.
- **Steps:**
  1. Focus `section.mkt-player` (named "Watch", `tabIndex=0`); press Space, `k`, `j`, `l`, ArrowLeft, ArrowRight, `m`, `,`, `.`, `5`, `f`, `f` (while fullscreen).
- **Expected:** Space and `k` call play/pause and `preventDefault` (no page scroll); `j` → 90, `l` → 100, ArrowLeft → 95, ArrowRight → 100; `m` flips `muted`; `,`/`.` step about 1/30 s; `5` → 300; `f` calls `requestFullscreen`, then `exitFullscreen`. Uppercase `K`/`J`/`L`/`M`/`F` (Caps Lock or Shift): wanted to work too (today ignored). Arabic keyboard layout: wanted to match on `e.code` (today `e.key` is Arabic letters, so letter shortcuts do nothing). Wanted: a visible shortcut list (`player.shortcuts` key exists but is unused).

### VPL-22 — Shortcuts do not fire from form controls or with modifiers

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** as VPL-21.
- **Steps:**
  1. Focus the "Playback speed" select; press ArrowRight and `-`.
  2. Focus the "← Back" button; press Space.
  3. Focus the player; press Ctrl+F / Cmd+F, Cmd+C.
  4. Focus the player; press `g` then `l`.
- **Expected:** step 1: the select changes its own value only; no seek and no speed double-change. Step 2: Space activates Back (navigates); it must not toggle playback and must not be blocked by `preventDefault`. Step 3: browser find / copy work; no fullscreen toggle or caption toggle. Step 4: the global `g l` leader goes to `/library` without also seeking +10 s first. Today the section handler checks none of these (no target or modifier check) — likely bug.

### VPL-23 — Speed select and +/- keys stay in sync

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** VPL-01 session.
- **Steps:**
  1. Select "1.5×".
  2. Focus the player; press `+` twice, then `-` four times.
- **Expected:** `video.playbackRate === 1.5` and select value 1.5; after `+` twice: 2 (capped, options 0.5, 0.75, 1, 1.25, 1.5, 2); after `-` four times: 0.75 (2 → 1.5 → 1.25 → 1 → 0.75). Select always shows the current rate. After switching video, wanted: rate re-applied to the new element (today `speed` state stays but the new `<video>` starts at 1×).

### VPL-24 — Subtitles: tracks loaded, `c` toggles, size select applies

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** `GET /api/videos/vid-1/subtitles` → `{ "items": [ { "id": 1, "language": "ar", "format": "vtt", "is_default": true, "url": "/api/videos/vid-1/subtitles/1.vtt" } ] }`.
- **Steps:**
  1. Open the player.
  2. Press `c`.
  3. Choose "Subtitle size" 150%.
- **Expected:** wanted: a `<track kind="subtitles" srclang="ar" src="..." default>` per subtitle; `c` toggles its mode between `showing` and `hidden`; the stage gets `--mkt-caption-scale: 1.5` and cues grow. Today the player never fetches subtitles or renders `<track>`, so `c` does nothing and only the CSS variable changes — likely bug (feature gap). Size select options: 75%, 100%, 125%, 150%.

### VPL-25 — Picture in picture toggle, unsupported is non-fatal

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** (a) `video.requestPictureInPicture` resolves; (b) `document.pictureInPictureElement` set and `document.exitPictureInPicture` resolves; (c) `requestPictureInPicture` undefined; (d) it rejects with `NotAllowedError`.
- **Steps:**
  1. Click "Picture in picture".
- **Expected:** (a) request called; (b) exit called; (c)(d) nothing thrown, no error UI. Wanted: hide the button when PiP is unsupported.

### VPL-26 — Session expiry (`expires_at`) during long playback

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`)
- **Preconditions / data:** session `expires_at` = now + 60 s; fake timers; e2e: real API with a short expiry.
- **Steps:**
  1. Play; advance past `expires_at`; trigger the next segment load (e2e) or a `stalled`/`error` event (unit).
- **Expected:** wanted: the player renews the session (new POST) before or when it expires and resumes at the same position, or shows the VPL-06 error with retry. Today `expires_at` is ignored (currently unhandled — likely bug).

### VPL-27 — RTL: Back arrow, chapter ticks vs. native scrubber

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`VideoPlayer.cases.test.tsx`; e2e-only: ticks vs. native scrubber)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; VPL-15 (d) chapters; e2e for visuals.
- **Steps:**
  1. Open the player in Arabic.
- **Expected:** `<html dir="rtl">`; labels "Back", "Playback speed", "Subtitle size", "Picture in picture" from `ar.json`; wanted: the Back arrow points right in RTL (today a hardcoded "←"). Ticks use `inset-inline-start`, so in RTL they are placed from the right — check they line up with the native scrubber (most browsers draw the native timeline left-to-right even in RTL, so ticks would be mirrored — likely bug). ArrowLeft/ArrowRight seek direction: confirm intended behaviour in RTL.

### VPL-28 — Phone width: controls wrap, video fits

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** e2e 375x812 portrait and 812x375 landscape.
- **Steps:**
  1. Open the player.
- **Expected:** video fits the width (no horizontal scroll); the control bar wraps; selects and buttons at least 44 px; `playsinline` keeps iOS from forcing fullscreen.

### VPL-29 — Light/dark theme: control bar readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** e2e; light and dark.
- **Steps:**
  1. Open the player in each theme.
- **Expected:** control bar labels, selects and buttons readable; chapter ticks visible against the stage background; focus ring on the player section visible.
