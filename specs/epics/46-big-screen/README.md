# Epic 46: Big-screen mode: a 10-foot view for the couch, in the desktop apps and the browser

## Why

Maktaba.app (macOS) and the Windows and Linux apps are Tauri shells around the web app. Plugged into
a TV, or on a big monitor across the room, that web app is a desk UI: 16 px text, a sidebar, hover
menus, small buttons, a native `<video controls>` bar, and nothing a TV remote or a game controller
can drive. The Apple TV and Android TV apps (Epic 14) have the 10-foot view, but a Mac mini or a
living-room PC under the TV has only the desk one.

Steam Big Picture and Kodi are the reference: one key turns the same app into a full-screen,
lean-back view that a remote, a controller, a keyboard's arrows or a mouse can all drive, with big
type, a visible focus, rows of posters, and a player whose controls are made for a D-pad.

## Goals

1. A big-screen mode in the web app (`/big`), full screen, that browses and plays the whole library:
   Home, Libraries, a library, Series, a video's page, Search, Live TV, Settings and the player.
2. Every flow works with the arrow keys / D-pad and Enter / OK and Back alone; with a game controller
   (Xbox and PlayStation layouts, through the Gamepad API); with media keys; and the mouse still works.
3. The Epic 14 10-foot rules (story 14.3): large type, a focus ring that never relies on colour alone,
   rows that keep horizontal moves in the row, Back that returns to the focus it left, a 5 % safe area
   for TVs that overscan, no swipe-only flow, no focus trap.
4. Entering and leaving is easy and safe: a "Big screen" button in the normal UI, a keyboard shortcut,
   a menu item in the desktop apps, an option to start the desktop app in big screen (saved on this
   machine), and Back at the top asks before it leaves.
5. English and Arabic, RTL-safe (in Arabic the layout and the navigation mirror), with a help page.
6. UI only: it reads the APIs the web app already uses, and every route keeps enforcing the user's
   role and library access server-side (`principal.AccessAllLibraries` / `principal.Libraries`).

## Non-goals

- A new design system or new API routes. Big screen reads `GET /api/home`, `/api/libraries/summary`,
  `/api/libraries/{id}/hub`, `/api/videos`, `/api/series*`, `/api/search*`, `/api/channels/now` and
  the streaming session API, exactly as the desk pages do.
- Admin pages (processing, users, settings beyond the viewer's own) in big screen: they stay in the
  desk UI. Big screen's Settings has only exit, language and playback preferences.
- Voice search, a TV launcher, HDMI-CEC. A remote that the OS presents as a keyboard or a gamepad
  works; anything else is out of scope.
- The phone layout. Big screen targets a landscape screen 1280 px wide and up (it still renders on a
  smaller one).
- Epic 45 phase 2 (favorites, playlists, On Deck, Up next, mark watched, facets): not on `main` when
  this epic was built. See *Follow-ups*.

## Design

### Route tree and shell

`/big/*` is one lazy-loaded route (`React.lazy`, its own chunk: `web/src/bigscreen/`), outside
`AppShell`, behind `RequireAuth`. App.tsx gains the one `<Route path="/big/*">`; `AppShell` gains
the entry button; the shortcut layer gains `g b` and stays out of the way under `/big`.

| Route | Screen |
|---|---|
| `/big` | **Home**: a hero for what to continue (the first Continue Watching card: Resume, Details), then rows: Continue Watching, Recently Added, Recently Watched, Live TV on now (`GET /api/channels/now`), one row per pinned library. Empty rows are hidden; an empty home says so and offers Libraries. |
| `/big/libraries` | **Libraries**: a grid of library cards (poster, name, video count). |
| `/big/library/:id` | **A library**: its hub rows (Continue Watching, Recently Added, Most Watched, Series), then a grid of all its videos, newest first, with Load more (`GET /api/videos?library=&sort=created_at&order=desc`). |
| `/big/series` | **Series**: a grid of every series the user can see (`GET /api/series`), with progress. |
| `/big/series/:id` | **A series**: its picture and name, Continue (the server's `next_episode`), a season picker, and its lessons with progress and watched marks. |
| `/big/video/:id` | **A video**: picture, title, length, description; Resume / Play from start (the video's `playback_state`); its chapters (each plays from there); a transcript excerpt (the first segments). |
| `/big/watch/:videoId?t=&series=` | **The player** (full screen). `series` names the series the video was opened from: the player's Next lesson. |
| `/big/search` | **Search**: a text field with an on-screen keyboard (English and Arabic layouts, space, delete, clear) that a physical keyboard also types into; results in titles and in transcripts (`POST /api/search`, mode `hybrid`); a transcript match plays from its time. |
| `/big/live` | **Live TV**: a grid of the channels with what is on now and its progress. |
| `/big/live/:number` | The live player (`pages/LiveTV/Player`, the desk one, with its routes kept under `/big`). |
| `/big/settings` | **Settings**: Exit big screen, language, playback preferences (play the next lesson automatically, subtitles on by default, subtitle size), and in the desktop apps "Start in big screen". |

The top of every screen but the player is a tab row: Home, Libraries, Series, Search, Live TV,
Settings, with the clock. The look is the Islamic library theme (PR 251) in its dark *Ink & Brass*
appearance whatever the desk theme: the library art behind a dark scrim, Amiri headings, IBM Plex
Sans Arabic text, brass-gold focus.

Sizes at 1920×1080 (they scale with the viewport, `clamp()` on `vw`): body 28 px, row titles 34 px,
screen titles 56 px, a poster card 384×216 (16:9), a library card 300 px wide. The page keeps a 5 %
inset on every side (overscan safe area); rows scroll inside it.

### Focus and navigation

A pure module, `bigscreen/focus/graph.ts`, decides where a move goes: given the focusable nodes (id,
rectangle, group) and a direction, it returns the next node, or none.

- Candidates lie in the direction of the move (their near edge past the current one's centre).
- Left / right stay in the current group (a row, a grid line, the tab row) when one there lies that
  way; at the row's end focus stays (no wrap, no jump to another row).
- Up / down go to the nearest group that way and land on the node it last had focused when that node
  is still there (focus memory, so going down and up returns to the same card), else on the node
  nearest the current one's centre across the axis.
- Scores: distance along the move plus twice the distance across it (the usual spatial-navigation
  weight, so a card straight down wins over a closer one off to the side).

Navigation is by on-screen geometry, so under `dir="rtl"`, where rows start on the right, the arrows
already mirror: Right moves to the card on the right, which is the previous one in an Arabic row. Only
the player's seek keys depend on the direction (below).

`FocusProvider` (React) feeds it: focusable elements carry `data-bs-focus` (and `data-bs-group` on
their container); at a key press it reads their rectangles in the top *layer* (`data-bs-layer`: a
dialog or a panel is a layer above the screen, so focus never leaves an open dialog and never gets
stuck under one), calls `graph.next`, focuses the result and scrolls it into view
(`scrollIntoView({block: "nearest", inline: "nearest"})`, padded so the ring is not cut). Focus moves
are real DOM focus, so screen readers and the browser's own Tab still work.

- **The ring**: 4 px brass outline plus a soft glow and a 1.04 scale (no scale under
  `prefers-reduced-motion`; the ring stays).
- **Back restores focus**: the focused element's id is saved per history entry (`location.key`);
  coming back to an entry focuses it again once it is on the page (a screen whose data loads late is
  watched until it is), else the screen's first control (`data-bs-autofocus`).
- **The mouse**: pointing at a focusable focuses it; a click is a click. The pointer hides after 3 s
  without moving (also in the desktop apps' full-screen window).
- **No traps**: a dialog's first control takes focus; Back closes it and focus goes back to what
  opened it.

### Input

One action vocabulary: `up down left right select back playPause play pause seekBack seekForward
next previous search menu`. Sources:

| Source | Map |
|---|---|
| Keyboard (`bigscreen/input/keys.ts`) | Arrows; Enter / NumpadEnter → select; Escape, Backspace (outside a text field), BrowserBack, GoBack → back; MediaPlayPause, MediaPlay, MediaPause, MediaTrackNext / Previous, MediaFastForward / Rewind; Space → playPause in the player (select elsewhere, as the browser does); `/` → search; ContextMenu → menu. IME composition is never an action. |
| Gamepad (`bigscreen/input/gamepad.ts`, `navigator.getGamepads()` polled per frame while one is connected) | The W3C *standard* mapping, the same indices for Xbox and PlayStation pads: A / ✕ → select, B / ○ → back, X / □ → playPause, Y / △ → search, LB / L1 and RB / R1 → previous / next (a page of a row; in the player −30 s / +30 s), LT / L2 and RT / R2 → seekBack / seekForward, View / Share → back, Menu / Options → menu, D-pad and the left stick (dead zone 0.5) → arrows. A held direction repeats after 400 ms, every 120 ms. The button hints name the pad's layout (A/B or ✕/○, read from the pad's id). |
| Media keys / the desktop tray's Play and Pause | the same actions (the tray's `media-play` / `media-pause` menu events). |
| Mouse | hover focuses, click selects; the player's controls show on movement. |

### The player

`/big/watch/:videoId` renders the existing `VideoPlayer` (the stream session, HLS / direct play,
signed-URL refresh, progress posts, stall and failure reports: unchanged) with a new `bare` prop: no
desk bar, no native controls, no desk key map. Over it, 10-foot controls:

- top: the title (and the series); bottom: a progress bar with chapter ticks, the time and the length,
  and a row of buttons: −30 s, −10 s, Play / Pause, +10 s, +30 s, Chapters, Subtitles, Audio, Next
  lesson (when there is one).
- Chapters, Subtitles (off, each track, and a size) and Audio (each track, when the engine lists them:
  `HTMLVideoElement.audioTracks` under native HLS, as in the Mac app's WebKit) open a side panel (a
  layer).
- Hidden after 4 s without input while playing; any input shows them. While hidden, Left / Right
  seek 10 s (in Arabic, where the progress bar runs right to left, Left is forward), Enter and Space
  play / pause, Up / Down show the controls.
- Back: closes a panel; else leaves the player (the page it came from, its focus restored).
- At the end, with a next lesson and "Play the next lesson automatically" on, an *Up next* card counts
  down 10 s (Play now / Cancel).
- Next lesson: the lesson after this one in the series the player was opened from (`?series=`, from
  `GET /api/series/{id}/episodes`).

Live TV plays in the desk live player (`pages/LiveTV/Player`), which already has a TV's keys (▲/▼
channels, digits, I for the guide), given a `base` prop so its own links stay under `/big`.

### Entering and leaving

- **Web (any browser, and the desktop apps)**: a "Big screen" button in the header, and `g b`. Entering
  saves where the user was and asks for full screen (`requestFullscreen`; refused without a gesture,
  which is harmless). Exit (Settings → Exit big screen, or Back at Home and confirm) leaves full
  screen and returns there.
- **Back at the top**: Back on Home asks "Leave big screen?" (Leave / Stay; Stay has the focus).
  Back on another top screen goes Home first.
- **Desktop shell** (`apps/desktop/src-tauri/src/bigscreen.rs`):
  - View → **Big Screen** (`CmdOrCtrl+Shift+B`), menu id `big-screen`: the window goes full screen
    and the page opens `/big` (the `menu` event, as every menu item; a page the shell cannot talk to,
    another machine's in client mode, is loaded at its `/big`).
  - View → **Start in Big Screen** (a check item), id `big-screen-start`: saved in `big-screen.json`
    beside `mode.json` (this machine's state folder: the app's config folder on a Mac and Linux,
    `%LOCALAPPDATA%` on Windows, so a roaming profile does not carry it). At start, with it on, the
    window opens full screen and the server's page loads at `/big`.
  - Commands for the page (`big_screen_state`, `set_big_screen` (full screen on / off),
    `set_big_screen_start`), granted to the bundled pages and to this machine's own server's pages
    (`capabilities/local-server.json`): nothing that reads or changes anything but the window and that
    one file.
  - The pointer hides when idle (the page's CSS).

### Strings and help

Every string is in `en.json` and `ar.json` under `big.*` (the shell's menu words in `lang.rs`,
English and Arabic). Help: a new topic `big-screen` (en / ar, the five standard sections) in *Watch*;
`PAGE_HELP` maps `/big/*` to it; the desktop app's help (mac-app) links to it.

## Stories

- **46.1** The spec, the epic index.
- **46.2** Focus engine: `focus/graph.ts` (pure, unit-tested: rows, grids, the tab row, focus memory,
  RTL, a lone card, nothing that way), `FocusProvider` (layers, restore on Back, mouse focus, idle
  pointer).
- **46.3** Input: `keys.ts` and `gamepad.ts` (pure, unit-tested: every key and button, the stick's dead
  zone, repeat, the layout from the pad's id), the polling hook.
- **46.4** Shell and screens: the `/big` layout and tab row, Home, Libraries, a library, Series (list
  and one), a video, Search with its keyboard, Live TV, Settings; the exit dialog. vitest per screen.
- **46.5** The player: `VideoPlayer`'s `bare` prop, the 10-foot controls, panels, idle hiding, next
  lesson and Up next; the live player's `base`. vitest.
- **46.6** Entry points: the header button, `g b`, the desktop menu items, "Start in big screen", the
  shell commands and capabilities; Rust tests (the setting's file, the start URL, the menu target).
- **46.7** i18n (en / ar), the help topic, browser verification at 1920×1080 in English and Arabic,
  keyboard only, on a branch stack (never the live database).

## Acceptance

- Every screen above is reachable and usable with the arrow keys, Enter and Escape alone, in English
  and Arabic, at 1920×1080; focus is always visible; Back from a video's page returns to the card that
  opened it.
- A standard gamepad (Xbox or PlayStation) moves, selects, goes back and controls the player.
- The player plays, pauses, seeks ±10 / ±30 s, jumps to a chapter, switches subtitles and plays the
  next lesson; its controls hide while watching and come back on any input.
- A viewer with one library sees only that library's rows, cards and search results (the API's rules,
  unchanged; big screen adds no route).
- The desktop app's View → Big Screen opens it full screen; with "Start in Big Screen" on, the next
  start opens in it; the setting stays on this machine.
- vitest for the focus graph, the key and gamepad maps and each screen; `cargo test` for the shell's
  setting; the help coverage test passes; typecheck, lint and prettier pass.

## Follow-ups

- **Epic 45 phase 2** (`feat/epic45-plex-parity`, not merged when this was built): once on `main`,
  Home gains its **On Deck** row, the player's Next lesson uses the play queue / Up next (and a video
  opened outside a series gets one), and a video's page gains Favorite and Mark watched.
- A big-screen Guide (the desk `/guide` grid at 10-foot size); today Live TV lists what is on now and
  the live player's mini-guide shows what is next.
- 4K type sizes (story 14.3's 36 pt): the sizes scale with the viewport already; a check on a 4K TV.
