# Live channel player

- **Route:** `/live/:number` (deep link by channel number, e.g. `/live/1`; every switch rewrites the URL: replace while surfing, push once a channel played 5 s)
- **Component:** `src/pages/LiveTV/Player.tsx`; from `@maktaba/tvplayer` (`web/packages/tvplayer/src`): `useTvSwitcher` (`react.tsx`, `switcher.ts`, `pipeline.ts`, `live.ts`, `prefetch.ts`, `gestures.ts`, `numberEntry.ts`, `ring.ts`), `keyToAction` (`keymap.ts`), `TuningCard`, `MiniGuide`, `NumberOverlay` (`overlay/index.tsx`), `SwitchLog` (`telemetry.ts`); remote path `RemoteSession` in `src/lib/remoteTv.ts`. `src/pages/LiveTV/useLiveHls.ts` is a one-element wrapper over `attachLive` that the player does not use (only its own test imports it).
- **API calls:** `GET /api/channels?enabled=true` → `{items: Channel[]}` via `channelsApi.list()`; `GET /api/channels/now` → wire `{server_time?, channels?: [{channel, current?, next?, on_air?}]}` mapped by `nowFrom()` to `{server_time, items: NowEntry[]}` via `channelsApi.now()` (both every 30 s); `GET /api/tv/info` → `TvInfo` (`remote.via_relay` picks LAN or remote) via `tvApi.info()`; LAN media `GET /stream/channel/{id}/index.m3u8` (`?retry=N` on retries) through native HLS or lazy `hls.js`; remote: `POST /api/tv/remote/tune` (200 live / 202 starting), `POST /api/tv/remote/sessions/{id}/heartbeat` every 60 s, `DELETE /api/tv/remote/sessions/{id}` on switch / unmount / `pagehide`. All in `src/lib/channels.ts` and `src/lib/remoteTv.ts`.
- **Access:** any signed-in user (`RequireAuth`); not admin-gated. Remote errors from the server (`remote_not_allowed` etc.) are shown as slates.
- **Existing tests:** `src/pages/LiveTV/Player.test.tsx` (mocks `api.get/post/delete`, `hls.js`, `HTMLMediaElement`) — covers: LIVE badge + LAN HLS URL, tuning card then tune banner with logo, retries then slate with direct link, hls.js fallback + fatal network error, no-HLS slate, banner now/next, mini-guide rows, not-found for `/live/9`, make-before-break on ArrowUp, debounced PageUp, PageDown wrap, digits + Enter and L, "No channel" overlay, mini-guide tap tunes, G opens guide, swipe surfs, prefetch of on-air neighbours only, remote tune + session end, remote 202 "Starting", 402 budget, 429 channels full. Package tests: `keymap.test.ts`, `switcher.test.ts`, `pipeline.test.ts`, `prefetch.test.ts`, `swipe.test.ts`; `useLiveHls.test.ts` (hook only). Gaps: bad route params, list/now failures, null lists, "Watch remotely" offer, watch from start, tap for sound, banner auto-hide, Escape, history push vs replace, crash recovery, permissions, RTL, a11y.

## Summary

| ID     | Title                                                            | Category      | Priority | Covered by existing test?                                   |
| ------ | ---------------------------------------------------------------- | ------------- | -------- | ----------------------------------------------------------- |
| LPL-01 | Plays the channel's LAN HLS with a LIVE badge                    | Happy path    | P1       | Yes (`Player.test.tsx`)                                     |
| LPL-02 | Tuning card shows at once, then the tune banner                  | Happy path    | P2       | Yes (`Player.test.tsx`)                                     |
| LPL-03 | Tune banner: now, progress, next; hides after 5 s; Enter toggles | Happy path    | P2       | Partial (`Player.test.tsx`: no auto-hide, no Enter)         |
| LPL-04 | Unknown channel number shows "Channel not found"                 | Navigation    | P1       | Partial (`Player.test.tsx`: "Open guide" not clicked)       |
| LPL-05 | Bad `:number` values (abc, 0, -1, 1.5) are not found             | Validation    | P1       | No                                                          |
| LPL-06 | Channel list request fails                                       | Error         | P1       | No                                                          |
| LPL-07 | Null or missing lists in list / now / info                       | Empty state   | P1       | No                                                          |
| LPL-08 | Offline channel: retries, then slate with a direct link          | Error         | P1       | Yes (`Player.test.tsx`)                                     |
| LPL-09 | hls.js path and fatal-error recovery                             | Error         | P2       | Yes (`Player.test.tsx`)                                     |
| LPL-10 | No HLS support at all shows a clear slate                        | Error         | P2       | Yes (`Player.test.tsx`)                                     |
| LPL-11 | Stall shows "Reconnecting…"                                      | Error         | P2       | No                                                          |
| LPL-12 | Channel up / down by keys and on-screen buttons                  | Happy path    | P1       | Partial (`Player.test.tsx`: keys only, buttons not clicked) |
| LPL-13 | Make before break: old channel plays until the new one starts    | Happy path    | P1       | Yes (`Player.test.tsx`)                                     |
| LPL-14 | Number entry, last channel (L / Backspace / button)              | Happy path    | P2       | Partial (`Player.test.tsx`: button and Backspace not)       |
| LPL-15 | Unknown number shows "No channel N"                              | Validation    | P2       | Yes (`Player.test.tsx`)                                     |
| LPL-16 | Modifier keys and text fields are ignored                        | Accessibility | P2       | Partial (`keymap.test.ts`: unit only)                       |
| LPL-17 | Mini-guide opens, tunes, closes with Escape                      | Happy path    | P2       | Partial (`Player.test.tsx`: Escape / close button not)      |
| LPL-18 | Guide key and "Open guide" button go to `/guide`                 | Navigation    | P2       | Partial (`Player.test.tsx`: G only)                         |
| LPL-19 | URL and history: replace while surfing, push after 5 s           | Navigation    | P2       | Partial (`Player.test.tsx`: pathname only)                  |
| LPL-20 | Remote path: tune, heartbeat, end session                        | Happy path    | P1       | Yes (`Player.test.tsx`)                                     |
| LPL-21 | Remote refusals show the right words                             | Error         | P2       | Partial (`Player.test.tsx`: 402, 429 only)                  |
| LPL-22 | Three LAN failures offer "Watch remotely"                        | Error         | P2       | No                                                          |
| LPL-23 | "Watch from beginning" opens the VOD at t=0                      | Happy path    | P2       | No                                                          |
| LPL-24 | Autoplay blocked shows "Tap for sound"; PiP failure is quiet     | Happy path    | P3       | No                                                          |
| LPL-25 | Loading text before the lineup arrives                           | Loading       | P3       | No                                                          |
| LPL-26 | Signed-out user is sent to `/login`                              | Permissions   | P1       | No                                                          |
| LPL-27 | Render crash shows the route error fallback; Back works          | Error         | P1       | No                                                          |
| LPL-28 | Phone width: controls wrap, swipe surfs                          | Responsive    | P3       | Partial (`Player.test.tsx`: swipe only)                     |
| LPL-29 | Arabic: RTL labels, up is still next channel                     | RTL           | P2       | Partial (`keymap.test.ts`: key map only)                    |
| LPL-30 | Light and dark theme overlays readable                           | Theme         | P3       | No                                                          |
| LPL-31 | Accessibility: labels, focus, hidden standby video               | Accessibility | P2       | No                                                          |

## Cases

Shared data (from `Player.test.tsx`): `lineup = {items:[{id:"c1",number:1,name:"One"},{id:"c2",number:2,name:"Two"},{id:"c3",number:3,name:"Three"}]}`; `nowPayload` with `c1` current "Now on One" (video_id `v1`), next "Next on One", `c2` current "Cartoons", `c3` off air; `lanInfo = {remote:{available:false, reason:"off", via_relay:false, max_streams:2, token_ttl_minutes:60}}`.

### LPL-01 — Plays the channel's LAN HLS with a LIVE badge

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Player.test.tsx` "shows a LIVE indicator and plays the channel HLS through the web port"
- **Preconditions / data:** shared data; native HLS (`canPlayType("application/vnd.apple.mpegurl")` → "maybe").
- **Steps:**
  1. Open `/live/1`.
- **Expected:** `data-testid="live-player"` with `data-path="lan"`. Badge "● LIVE". Active `data-testid="live-video"` src `/stream/channel/c1/index.m3u8`. No `POST /api/tv/remote/tune`.

### LPL-02 — Tuning card shows at once, then the tune banner

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes — `Player.test.tsx` "shows the tuning card at once, then the channel logo in the tune banner"
- **Preconditions / data:** shared data; channel 1 `logo_url:"/api/channels/c1/logo"`.
- **Steps:**
  1. Open `/live/1`; fire `playing` on the active video.
- **Expected:** Before the first frame the tuning card shows "1", "One", "Now on One", status "Tuning…". After `playing`, `data-testid="tune-banner"` shows the logo, "1", "One".

### LPL-03 — Tune banner: now, progress, next; hides after 5 s; Enter toggles

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Player.test.tsx` "renders a tune banner with current and next program" (no auto-hide, no Enter)
- **Preconditions / data:** shared data; fake timers.
- **Steps:**
  1. Play `/live/1` to first frame.
  2. Advance 5 s.
  3. Focus `live-player`, press Enter; press Enter again.
  4. Press I (mini-guide open), advance 10 s.
- **Expected:** Banner shows "Now on One", a progress bar labelled "Duration", "Next: Next on One". After 5 s the banner is gone. Enter shows it, Enter again hides it. While the mini-guide is open the banner does not show and the hide timer does not run.

### LPL-04 — Unknown channel number shows "Channel not found"

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Partial — `Player.test.tsx` "shows a not-found state for an unknown channel number" (button not clicked)
- **Preconditions / data:** shared data.
- **Steps:**
  1. Open `/live/9`.
  2. Click "Open guide".
- **Expected:** `ErrorState` "Channel not found" / "That channel does not exist, is disabled, or you cannot access it." No request to `/stream/...`. Click goes to `/guide`.

### LPL-05 — Bad `:number` values (abc, 0, -1, 1.5) are not found

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** shared data; also a lineup where a channel has `number: 0`.
- **Steps:**
  1. Open each of `/live/abc`, `/live/0`, `/live/-1`, `/live/1.5`, `/live/%20`, `/live/1e0`.
- **Expected:** Each shows "Channel not found" with "Open guide"; no stream fetch; no crash. `/live/1e0` and `/live/01` resolve to channel 1 (Number parsing) — confirm this is acceptable or canonicalise the URL. `/live/0` only plays if a channel really has number 0.

### LPL-06 — Channel list request fails

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/channels?enabled=true` rejects with `ApiError` 500; repeat with 401, 403, a network `TypeError`, and a 200 with malformed JSON body.
- **Steps:**
  1. Open `/live/1`.
- **Expected:** A clear error state ("Something went wrong." with a retry) — not a blank page. Today the `.catch` sets `lineup=[]` so every failure shows "Channel not found", which is misleading (currently unhandled — likely bug). The 30 s refresh should recover and start playing once the list loads.

### LPL-07 — Null or missing lists in list / now / info

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** run per payload:
  - `GET /api/channels?enabled=true` → `{ "items": null }` and `{}` (fields: `items`)
  - `GET /api/channels/now` → `{ "server_time":"<now>", "channels": null }`, `{}`, and an entry `{ "channel": {...c1}, "current": null }` with `next` missing (fields: `channels`, `channels[].current`, `channels[].next`)
  - `GET /api/tv/info` → `{}` (no `remote`), and `{ "remote": null }`
- **Steps:**
  1. Open `/live/1`; press I for the mini-guide.
- **Expected:** `items` null/missing: "Channel not found" (never a thrown error / blank). `now` null/missing: player plays channel 1; banner says "No content" and has no "Next"; mini-guide opens with no rows (and does not crash); no "Watch from beginning". `info` without `remote`: LAN path (`data-path="lan"`).

### LPL-08 — Offline channel: retries, then slate with a direct link

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes — `Player.test.tsx` "retries a failing stream, then shows a slate with a direct link"
- **Preconditions / data:** `fetch` of the playlist / media `error` fails every time; `LIVE_RETRIES` attempts at `LIVE_RETRY_MS`.
- **Steps:**
  1. Open `/live/1`; advance through all retries.
- **Expected:** Retry URLs carry `?retry=1`, `?retry=2`… Then `data-testid="live-slate"` with "Channel unavailable — no content right now." and `data-testid="live-direct-link"` → `/stream/channel/c1/index.m3u8`; videos hidden.

### LPL-09 — hls.js path and fatal-error recovery

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes — `Player.test.tsx` "plays through hls.js when the browser has no native HLS"
- **Preconditions / data:** `canPlayType` → ""; `Hls.isSupported()` true.
- **Steps:**
  1. Open `/live/1`; emit `hlsError {fatal:true, type:"networkError"}`.
- **Expected:** hls.js `loadSource` with the channel URL; after the fatal error the switcher retunes (no slate unless retries run out).

### LPL-10 — No HLS support at all shows a clear slate

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes — `Player.test.tsx` "shows a clear slate when neither native HLS nor hls.js can play"
- **Preconditions / data:** no native HLS, `Hls.isSupported()` false.
- **Steps:**
  1. Open `/live/1`.
- **Expected:** Slate "This browser can't play live streams." plus "Your browser may not play live streams. Open the stream in a player such as VLC: stream link".

### LPL-11 — Stall shows "Reconnecting…"

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No (`useLiveHls.test.ts` covers the hook's `reconnecting` flag, not the page)
- **Preconditions / data:** playing channel 1; fake timers; fire `waiting`/`stalled` on the active video and advance past `STALL_MS`.
- **Steps:**
  1. Play to first frame, then stall.
  2. Fire `playing` again.
- **Expected:** `data-testid="live-reconnecting"` with `role="status"` "Reconnecting…" appears during the stall and goes away once playing again. Not shown when the slate is up.

### LPL-12 — Channel up / down by keys and on-screen buttons

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Partial — `Player.test.tsx` covers ArrowUp, PageUp ×2 (debounced), PageDown wrap; the buttons are not clicked
- **Preconditions / data:** shared data; playing channel 1.
- **Steps:**
  1. Click "Channel up" (`aria-label="Channel up"`).
  2. Click "Channel down" twice.
  3. Focus the player; press `ChannelUp`, `MediaTrackNext`, keyCode 427 (Tizen), ArrowDown.
  4. Lineup of a single channel: press ArrowUp.
- **Expected:** Each tune lands 300 ms after the last press; one tune in flight. Up from 3 wraps to 1, down from 1 wraps to 3. With one channel, up stays on it and nothing breaks.

### LPL-13 — Make before break: old channel plays until the new one starts

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Player.test.tsx` "keeps the old channel playing until the new one can play (ArrowUp)"
- **Preconditions / data:** playing channel 1.
- **Steps:**
  1. Press ArrowUp; check both videos; fire `playing` on the standby.
- **Expected:** Tuning card for channel 2 over the still-playing channel 1; the standby gets channel 2's URL; after its first frame the elements swap and the URL is `/live/2`. Never a black screen.

### LPL-14 — Number entry, last channel (L / Backspace / button)

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Player.test.tsx` "tunes by number with Enter and goes back with L"
- **Preconditions / data:** playing channel 1; fake timers.
- **Steps:**
  1. Press 3 then Enter.
  2. Press Backspace.
  3. Press 2 and wait 1.5 s (no Enter).
  4. Click "Last channel".
  5. Press Numpad3 with NumLock off (`code:"Numpad3"`).
- **Expected:** Number overlay shows "3" while typing; tunes 3; Backspace returns to 1; "2" auto-commits after 1.5 s; "Last channel" button appears only once there is a last channel and goes back; numpad code tunes 3.

### LPL-15 — Unknown number shows "No channel N"

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes — `Player.test.tsx` "says there is no such channel for an unknown number"
- **Preconditions / data:** playing channel 1.
- **Steps:**
  1. Press 4, 2, Enter.
- **Expected:** Overlay "No channel 42"; channel 1 keeps playing; URL stays `/live/1`.

### LPL-16 — Modifier keys and text fields are ignored

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Partial — `keymap.test.ts` "leaves browser shortcuts and unknown keys alone", "recognises text fields" (unit only)
- **Preconditions / data:** playing channel 1.
- **Steps:**
  1. On the player press Ctrl+L, Cmd+G, Alt+ArrowUp.
  2. Press Backspace while focus is in an input inside the page (e.g. a search field in the app header).
- **Expected:** No tune, no navigation to `/guide`, browser shortcut not prevented. Backspace in a text field edits text and does not switch channel.

### LPL-17 — Mini-guide opens, tunes, closes with Escape

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Partial — `Player.test.tsx` "opens a mini-guide listing channels with now and next" and "tunes from the mini-guide (I, then a tap)"
- **Preconditions / data:** shared data.
- **Steps:**
  1. Click "Mini-guide"; check rows.
  2. Press Escape.
  3. Press I; click its "Close" button.
  4. Press I; click channel 2.
- **Expected:** `role="dialog"` named "What's on now" with rows "1 One — Now on One — Next on One", "2 Two — Cartoons", "3 Three — No content"; the current row has `aria-current="true"`. Escape and "Close" close it. Picking channel 2 closes it and tunes 2.

### LPL-18 — Guide key and "Open guide" button go to `/guide`

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial — `Player.test.tsx` "opens the full guide with G"
- **Preconditions / data:** playing channel 1; remote path variant.
- **Steps:**
  1. Click "← Open guide".
  2. (remote) Open `/live/1` with `via_relay:true`, then press G.
- **Expected:** `/guide` renders. In remote mode the session is ended (`DELETE /api/tv/remote/sessions/{id}`) when the player unmounts.

### LPL-19 — URL and history: replace while surfing, push after 5 s

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial — `Player.test.tsx` checks the pathname after switches, not push vs replace
- **Preconditions / data:** `MemoryRouter` history `["/guide", "/live/1"]`; fake timers.
- **Steps:**
  1. Play 1; within 5 s press ArrowUp (settles on 2); press Back.
  2. Play 1; wait 6 s; press ArrowUp (settles on 2); press Back.
- **Expected:** Case 1: Back goes to `/guide` (2 replaced 1). Case 2: Back goes to `/live/1` and the player tunes 1 without a reload. Forward returns to `/live/2`.

### LPL-20 — Remote path: tune, heartbeat, end session

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Player.test.tsx` "tunes remotely and ends the session on a switch and on unmount", "shows Starting remote stream while the push starts (202)"
- **Preconditions / data:** `GET /api/tv/info` → `remote:{available:true, via_relay:true, ...}`; `POST /api/tv/remote/tune` → 200 `{session_id, hls_url, ...}`.
- **Steps:**
  1. Open `/live/1`; advance 60 s; switch to 2; unmount; also dispatch `pagehide`.
- **Expected:** `data-path="remote"`, badge "LIVE · Remote". One heartbeat after 60 s swaps the token without reloading the media. Switching ends the old session; unmount and `pagehide` end the current one. A 202 shows "Starting remote stream…". No neighbour prefetch on the remote path.

### LPL-21 — Remote refusals show the right words

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Partial — `Player.test.tsx` covers 402 `budget_exhausted` and 429 `remote_channels_full`
- **Preconditions / data:** `POST /api/tv/remote/tune` rejects with `ApiError` carrying `code`: `user_streams_full`, `remote_storage_full`, `remote_unavailable` (message "no inputs"), `remote_not_allowed`, `remote_timeout`, `no_such_channel`, an unknown code.
- **Steps:**
  1. Open `/live/1` for each.
- **Expected:** Slate text: "Too many remote streams"; "All remote channels are in use (the Cloudflare storage is full)"; "Remote viewing is not set up (no inputs)"; "You may not watch away from home"; "The remote stream did not start. Try again in a moment."; "Channel not found"; unknown → "Can't reach the remote stream host (\*.cloudflarestream.com)…". No direct link on the remote path. Heartbeat 410 `session_ended` retunes.

### LPL-22 — Three LAN failures offer "Watch remotely"

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `info.remote = {available:true, via_relay:false}`; LAN stream fails every time.
- **Steps:**
  1. Open `/live/1`; let three tunes fail (`LAN_FAILS_TO_OFFER = 3`).
  2. Click "Watch remotely" (`data-testid="watch-remote"`).
  3. Variant: after the first failure `GET /api/tv/info` answers `via_relay:true`.
- **Expected:** After the 3rd failure the button shows (on the slate, or in the toolbar when not degraded). Clicking it switches to `data-path="remote"` and calls `POST /api/tv/remote/tune`. In the variant the player moves to remote on its own. With `available:false` the button never shows.

### LPL-23 — "Watch from beginning" opens the VOD at t=0

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** shared data; the now payload's `c1.current` carries `video_id:"v1"` and is airing.
- **Steps:**
  1. Play `/live/1`; click "Watch from beginning".
- **Expected:** Navigates to `/videos/v1/watch?t=0`. Note: `fromBlock()` in `src/lib/channels.ts` never copies `video_id` (`WireBlock` has no such field), so today `current.video_id` is always undefined and the button never shows (currently unhandled — likely bug).

### LPL-24 — Autoplay blocked shows "Tap for sound"; PiP failure is quiet

- **Priority:** P3
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** `HTMLMediaElement.prototype.play` rejects `NotAllowedError` once, then resolves; `requestPictureInPicture` rejects.
- **Steps:**
  1. Open `/live/1`; click "Tap for sound" (`data-testid="live-unmute"`).
  2. Click "Picture in picture".
- **Expected:** Video starts muted and the button shows; clicking it sets `muted=false` and calls `play()`. PiP rejection shows nothing and logs no uncaught error.

### LPL-25 — Loading text before the lineup arrives

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/channels?enabled=true` never resolves.
- **Steps:**
  1. Open `/live/1`.
- **Expected:** "Loading…" shown. Should be `role="status"` (currently a plain `<p>` — a11y gap). No stream request before the lineup and info are known.

### LPL-26 — Signed-out user is sent to `/login`

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** full `App`; `useAuth` → `{user:null, loading:false}`; also a normal (non-admin) user.
- **Steps:**
  1. Navigate to `/live/1`.
- **Expected:** Signed out: redirect to `/login` with `state.from="/live/1"`; no channel, now, info or stream requests. Normal user: plays (not admin-gated).

### LPL-27 — Render crash shows the route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** full `App`, history `["/guide", "/live/1"]`. Force a render throw: mock `channelsApi.now` (bypassing `nowFrom`) to resolve `{server_time:"<now>", items:[{channel:null}]}` (the `nowById` loop reads `e.channel.id`), or mock `TuningCard` to throw.
- **Steps:**
  1. Render at `/live/1`.
  2. Press Back; then repeat and click a nav link instead.
- **Expected:** Route-level error fallback (boundary in `src/components/AppShell.tsx`), not a blank page. Back shows the guide without a reload; a nav link renders its page. Any remote session is ended and timers stop (no requests after leaving).

### LPL-28 — Phone width: controls wrap, swipe surfs

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** Partial — `Player.test.tsx` "surfs with a vertical swipe on the picture"
- **Preconditions / data:** viewport 375 × 812 (mobile preset, touch).
- **Steps:**
  1. Open `/live/1`; swipe up on the picture; tap the picture.
- **Expected:** No horizontal page scroll; the video fills the width; the controls toolbar wraps and every button is reachable. Swipe up = next channel; a tap opens the mini-guide; native controls stay usable.

### LPL-29 — Arabic: RTL labels, up is still next channel

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Partial — `keymap.test.ts` "keeps up = next channel in RTL (nothing is mirrored)"
- **Preconditions / data:** `localStorage["mkt:locale"]="ar"`; Arabic channel names.
- **Steps:**
  1. Open `/live/1`; press ArrowUp; open the mini-guide.
- **Expected:** `dir="rtl"`; buttons and badge use Arabic strings from `ar.json` (`live.*`); ArrowUp still goes to channel 2; banner and mini-guide mirror; channel numbers and Arabic titles render correctly.

### LPL-30 — Light and dark theme overlays readable

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No (visual)
- **Preconditions / data:** `data-theme="light"` and `"dark"`.
- **Steps:**
  1. Show the tuning card, banner, mini-guide, slate and number overlay.
- **Expected:** All overlays readable over video in both themes; no hardcoded colours outside tokens.

### LPL-31 — Accessibility: labels, focus, hidden standby video

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** playing channel 1.
- **Steps:**
  1. Tab through the page.
- **Expected:** The player `section` is focusable (`tabIndex=0`) with name "Live channel" and a visible focus ring, so keys work after a click. Toolbar `role="toolbar"` named "Player controls"; "Channel up"/"Channel down"/"Last channel" have `aria-label`. The standby video has `aria-hidden="true"`, `tabIndex=-1` and no controls. The number overlay is `role="status" aria-live="polite"`; "Reconnecting…" is `role="status"`. Mini-guide dialog: focus moves into it and back to the player on close.
