# Story 13.21 — The video engine as a setting in the Player

> Epic 13 · Build · Follows 13.18 (PR #1591) · Depends on: 13.18 (the Player plays with
> libmpv) · Size: S

## Why

Story 13.18 keeps JavaFX's engine inside the Player while libmpv proves itself on every
machine, but the only way to choose it on purpose is an environment variable,
`MAKTABA_PLAYER_ENGINE=javafx` (`mpv/LibMpv.kt`). A person whose video stutters, stays black or
has no sound on libmpv cannot try the older engine without a terminal or a shortcut edited by
hand, and the notice shown when libmpv fails to load (`engine.fallback`) says to reinstall but
links nowhere.

## Description

**The setting.** Profile → Playback gets **Video engine**, after the quality row:
- **Automatic** (the default): libmpv, and JavaFX only when libmpv cannot load, as 13.18;
- **Older engine**: JavaFX and the `desktop-player` profile, whatever libmpv can do.

Under it, one line: **Use the older engine only if videos do not play well. The home converts
more files for it, so they take longer to start.** and **Learn more**, which opens the home's
help page at `/help/desktop-player#video-engine` in the browser.

- Kept in the Player's own settings file (`player.engine`: `auto` or `javafx`), not in the
  shared `PlaybackValues`: it is this Player's alone.
- It applies to the next video or channel opened; one playing keeps its engine.
- **The environment variable stays** for support and goes first: with
  `MAKTABA_PLAYER_ENGINE=javafx` the row shows **Older engine**, disabled, with **Set by
  `MAKTABA_PLAYER_ENGINE` on this computer.**
- **When libmpv cannot load**, the row shows **Older engine**, disabled, with **The new engine
  could not start: {reason}.** and **Learn more**.

**The notice.** 13.18's one-time notice when libmpv fails to load keeps its words and gains
**Learn more** (the same help section). It is shown once per Player version and reason, not on
every start; choosing the older engine on purpose shows none.

**`Engine`** (`mpv/Engine.kt`) reads the choice at each open instead of once per process:
`players()` returns `MpvPlayers` or `FxPlayers` by the environment variable, then the setting,
then whether libmpv loaded. `describe()` and the problem report's line say which engine played
and why: **automatic**, **chosen in Profile**, **`MAKTABA_PLAYER_ENGINE`** or **libmpv did
not load: {reason}**.

**The help page** (`web/src/help/content/{en,ar}/desktop-player.md`) gets **The video engine**
under "When something goes wrong" (`#video-engine`): what each choice does, when to use the
older one, that `MAKTABA_PLAYER_ENGINE=javafx` does the same for support, how to send a
problem report from **Help and about**, and that the older engine leaves in the release story
13.22 names. The FAQ's sentence on what the home sends this Player (H.264 at most 1080p) is
brought up to 13.18's direct play.

**When JavaFX leaves** (story 13.22's removal), the row and its strings leave with it, a
stored `javafx` reads as **Automatic**, and the help section says the Player has one engine
from that version.

**maktaba.dev.** The Apps page's Player text names the setting, in English and Arabic, with a
screenshot of Profile → Playback on test data.

## Acceptance criteria

- **Given** **Older engine** chosen, **when** the next video opens, **then** it plays with
  JavaFX and its session asks for `desktop-player`.
- **Given** **Automatic** chosen again, **then** the next video plays with libmpv and
  `desktop-mpv`; the one playing is not reopened.
- **Given** `MAKTABA_PLAYER_ENGINE=javafx`, **then** the row shows **Older engine** disabled
  with its line, and JavaFX plays.
- **Given** libmpv cannot load, **then** the notice shows once with **Learn more**, and the row
  shows the reason, disabled.
- **Given** **Learn more**, **then** the browser opens the home's help page at
  `#video-engine`, in the Player's language.
- **Given** a problem report, **then** its log names the engine and why.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`EngineTest`) | the four inputs: env set, setting `javafx`, setting `auto` with libmpv loaded, `auto` with libmpv missing | `players()`, `describe()` | `FxPlayers` and "`MAKTABA_PLAYER_ENGINE`"; `FxPlayers` and "chosen in Profile"; `MpvPlayers` and "automatic"; `FxPlayers` and the reason |
| T02 | unit (`PlaybackMpvTest`) | a video playing with libmpv | change the setting, open the next | the first not reopened; the next with `desktop-player` |
| T03 | unit | the notice | start twice with the same version and reason; a new version | shown, not shown, shown |
| T04 | unit (Compose UI test) | Profile → Playback in each of the four states | read the row | the chosen value; disabled with its line for env and missing |
| T05 | unit | a store with `player.engine=javafx` after JavaFX leaves (13.22) | read | **Automatic** |
| T06 | screenshot (13.17) | Profile → Playback, en and ar; the row disabled by a missing libmpv | capture | match their references |
| T07 | unit | the help page | the help content test (`web/src/help/Help.test.tsx`) | `#video-engine` in en and ar |

## Edge cases

- A machine where libmpv loads but a file plays badly: the person chooses the older engine;
  their problem report shows libmpv played the earlier video and JavaFX the later one.
- A Player with no home signed in: **Learn more** opens maktaba.dev's Player page instead.
- The setting changed while Live TV plays: the channel keeps playing; the next channel opened
  follows the setting (after 13.22; before it, Live TV is JavaFX either way).
- A settings file written by a newer Player with an engine value this one does not know: read
  as **Automatic**.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/…/mpv/Engine.kt` | the choice read at each open; `describe()` with why |
| `apps/desktop-player/src/main/kotlin/…/Playback.kt` | `players` from `Engine.players()` per open |
| `apps/desktop-player/src/main/kotlin/…/profile/PlaybackSettingsPage.kt`, `ProfileStrings.kt`, `ProfileState.kt` | the row, its states and lines, en and ar |
| `apps/desktop-player/src/main/kotlin/…/Main.kt`, `Strings.kt` | the notice once per version and reason, **Learn more** |
| `apps/desktop-player/src/test/…` | T01–T05 |
| `web/src/help/content/{en,ar}/desktop-player.md` | **The video engine**; the FAQ's codec sentence |
| `web/packages/devportal/scripts/apps.mjs` | the setting, en and ar, a screenshot |
