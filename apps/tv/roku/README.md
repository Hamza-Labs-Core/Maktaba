# `apps/tv/roku/`: the Roku channel (Epic 58)

Roku runs neither web nor Android apps: a channel is BrightScript and SceneGraph. Roku also has
no right-to-left text (research.md §6.4): it draws a Label's characters left to right, unjoined.
What every Roku screen needs for Arabic is story 58.15's; the channel's screens are 58.12's, and
its player and certification build on them in 58.13.

## The channel (story 58.12)

A household signs in once (a cloud code its phone approves, a home found on the network, or the
home's address) and the next launch opens that home's Home. Home, the libraries with their five
tabs, Play and Shuffle, the letter rail, a series, search, a video's Resume or Start over and the
account page follow the TV layout of Epic 56 §6.5 on Roku's keys: OK opens, Play plays, `*` opens
a card's actions, Back steps out (a tab's top, then the tabs, then the screen; on the first
screen it asks before leaving). Deep links (`contentId` with `mediaType` movie, episode, series
or shortFormVideo) play the video, at launch or while running, or open Home with the
not-available line. Nothing is sold in the channel.

| Path | What |
|---|---|
| `manifest`, `source/main.brs` | the channel; Main forwards launch and input (ECP) deep links to the scene |
| `source/net.brs`, `source/http.brs` | the home's API client: the client header, one refresh and one retry on 401, sign-out on a refused refresh; reaching a home (direct HTTPS names, plain HTTP on the LAN, the cloud's connections) and the /24 probe |
| `source/compat.brs`, `source/deeplink.brs`, `source/nav.brs`, `source/store.brs` | the compat check (58.01), the deep-link router, Back's steps, the grid's memory window, the registry (homes, tokens, language) |
| `components/MainScene.*` | the screen stack, the session (the scene's HTTP agent carries the bearer for posters), deep links, the leave dialog |
| `components/tasks/` | `ApiTask` (one thread for the API), `HomeTask` (connect, probe, read an address), `CodeTask` (the cloud code and the home's own code) |
| `components/screens/`, `components/widgets/` | the screens and their parts (`PosterCard`, `PillRow`, `TvButton`, the dialogs) |
| `components/common.brs` | what the screens share: labels in the bundled font, mirrored placement, rows and grids in reading order |
| `components/theme.brs` | the dark colours, generated from `web/design-system/tokens/` by `make build-tokens` (never edited by hand) |
| `tools/build.mjs` | the build (T02): BrighterScript with bslint, fonts and images, the zip under 4 MB; `--test` for the screen tests, `--device-tests` for checklist row 1 |

## The player (story 58.13)

The player opens a stream session with `client_profile: roku` and this Roku's `device_caps`
(58.02), built from roDeviceInfo's answers by `source/caps.brs`; the home decides from them what
plays directly, what it remuxes (AV1 by DASH, the only way Roku plays it) and what it
transcodes. The Video node's own bar is off (`enableUI="false"`): Maktaba's overlay shows the
title, series, chapter and position, a thumbnail from the home's trickplay sheet while seeking
(Left and Right 10 s, mirrored in Arabic; Rewind and Fast forward 30 s), and the Audio and
Subtitles pickers. An Arabic subtitle is drawn by the channel's own Label (the sidecar WebVTT,
fetched with the session's `stream_sig`, each cue shaped through VisualText); any other
language by Roku's renderer, which follows Roku's caption settings. With no subtitle chosen and
Roku's captions on, the track in the channel's language shows. Progress goes to the home every
10 s and on pause, seek, the end and Back, and the session is closed on Back. Play all and
Shuffle walk the server queue; Up next shows 15 s before the end and OK plays it. A Video error
retries once with `force_transcode`; a second, or a session the home refuses, shows the error
with the home's reason and Back.

| Path | What |
|---|---|
| `source/caps.brs` | `CapsAnswers()` (roDeviceInfo on the device) and `CapsFromAnswers(answers)` (the report, tested against shared/playback/caps-vectors.json) |
| `source/player.brs` | the player's logic: stream format, subtitle choice and renderer, WebVTT, Up next, the retry, the queue, chapters, trickplay tiles, seeking |
| `components/screens/PlayerScreen.*`, `components/widgets/ChoiceDialog.*` | the Video node, the overlay, the subtitle layer, the pickers |
| `.github/workflows/roku-release.yml` | a `v*` tag whose channel changed attaches `maktaba-roku-<version>.zip` to the release (`node tools/build.mjs --version X.Y.Z`); signing it is on a Roku (docs/stores/roku.md) |

**In Arabic** every screen is placed through `ui_Mirror` (x positions from the English layout,
mirrored), rows and grids get their items in reading order and start on the right-most card,
row titles are moved to the row's right edge, and a short row is padded at its logical end with
blank cards that never take the focus.

**Tests** (`npm test`; CI's Roku job):

- the logic on brs-engine (T01): `tests/channel_test.brs` and the compat vectors;
- the player's logic (58.13 T01–T03): `tests/player_test.brs`, the capability report against
  58.02's vectors and Arabic cues against 58.15's (`tests/player.test.mjs`);
- the build (T02): `tests/build.test.mjs`, also running the device-test package;
- the screens (T03): `tests/screens.test.mjs` builds a test package (`--test`) pointed at
  `tests/mockhome.mjs` (a mock home with 80 videos, a series, collections and a playlist, and a
  mock cloud that approves a code), runs it on brs-node's SceneGraph with ECP, sends keys,
  follows the focus through the channel's log and compares screenshots with `tests/screens/`
  (the player's among them: its overlay, pickers, an Arabic cue, a seek, Up next, a refused
  session)
  (English and Arabic, dark; a mean difference under 12 on a 480×270 copy). After a deliberate
  change: `UPDATE_SCREENS=1 node --test tests/screens.test.mjs`, look at every image, commit.
  Find on this network runs on Linux only (macOS has no 127.0.0.x addresses for the probe; in
  Docker: `docker run --rm -v "$PWD":/repo -v /repo/apps/tv/roku/node_modules -w
  /repo/apps/tv/roku node:24-bookworm bash -c 'npm ci && npm test'` from the repository root).

**What brs-engine is not** (each a row of the real-device checklist in docs/stores/roku.md):
Roku's renderer, fonts and timing; the Video node; posters fetched with the scene's HTTP agent
(brs-engine sends no bearer for them, so the mock home serves them openly); a RowList's
unfocused rows keeping their own position. brs-node draws text with a canvas library that runs
the bidi algorithm, which a Roku never does; `tests/roku_text.cjs`, preloaded by the tests,
turns that off so Arabic reads as it will on a Roku.

| Path | What |
|---|---|
| `source/visualtext.brs` | `VisualText(s, maxWidthChars)`, `VisualTextDir`, `VisualLines`, `VisualIsRtl`: text from the home (titles, descriptions, names, typed queries, subtitle cues) shaped into joined presentation forms and put in display order on the device |
| `source/visualtext_tables.brs` | its data, generated by `tools/gen_tables.py` (never edited by hand) |
| `source/layout.brs` | `LayoutNew(rtl, width)`: x positions, label alignment, row order, start focus, keys and focus maps, mirrored in Arabic |
| `source/i18n.brs` | `I18nNew(lang)`: the channel's catalogs in its own language; `Tr(key)` for static strings, `Format(key, args)` for templates |
| `i18n/{en,ar}.json` | the catalogs; en.json's `@key` metadata gives each label's size (`widthChars`, `lines`) |
| `locale/{en_US,ar_SA}/` | generated by `tools/shape_strings.py`: `strings.json` (what `I18nNew` reads, Arabic already visual) and Roku's `translations.ts` |
| `tools/visual.py` | the reference: python-bidi and arabic-reshaper (pinned in `tools/requirements.txt`), the source of every expected output |
| `tools/gen_vectors.py` | writes the expected outputs of `shared/i18n/visual-vectors.json` |
| `tests/` | the BrightScript tests on brs-engine (`npm test`), their logs in `build/logs/` |

## How Arabic reaches the screen

The same six steps run in the reference and on the device: normalise whitespace, keep in logical
order any string with a character the device cannot shape (Persian or Urdu letters outside the
table, Hebrew, ZWJ, ZWNJ, embedding controls; logged), drop tashkeel (Roku has no mark
positioning), cut to the width with an ellipsis, join letters (lam-alef ligatures included), then
UAX #9 for one line with mirrored brackets. The vector file holds the device to the reference,
case by case; a difference fails CI.

- **Static strings** are shaped at build time. Change `i18n/*.json`, then run
  `tools/shape_strings.py`: it fails, naming the key, when a string does not fit its label.
- **Text from the home** is shaped on the device. Shape it once, where the data is loaded (the
  Task that fetches a row), and keep the visual string in the ContentNode: item components are
  recycled as a row scrolls, and shaping in them repeats the work. On brs-engine a 229-character
  description takes about 16 ms; story 58.15's budget, 50 ms a screen, is re-measured on a Roku
  Express (docs/stores/roku.md, row 10).
- Arabic multi-line strings carry their own line breaks (lines are broken before reordering, so
  the first line holds the start of the text): their Label needs `wrap="true"` and enough
  `maxLines`, and must not be wrapped again.
- Screens place every component through `LayoutNew(i18n.rtl, width)`; `RowList` and
  `PosterGrid` get `layout.Order(items)` and start focus at `layout.StartIndex(count)`.

## Commands

From the repository root:

    R="uv run --no-project --python 3.12 --with-requirements apps/tv/roku/tools/requirements.txt"
    $R python apps/tv/roku/tools/shape_strings.py         # after a catalog change
    $R python apps/tv/roku/tools/gen_vectors.py           # after adding a vector's input
    $R python apps/tv/roku/tools/gen_tables.py            # after a reference change
    $R pytest apps/tv/roku/tools/tests -q                 # the reference and the build step
    (cd apps/tv/roku && npm ci && npm test)               # BrightScript on brs-engine
    (cd apps/tv/roku && node tools/build.mjs)             # the store zip, build/maktaba-roku.zip

CI runs all of it in the Roku job (`.github/workflows/_roku.yml`), keeping brs-engine's output.

The BrightScript is plain `.brs` (valid BrighterScript), so brs-engine runs it without a compile
step; the channel's BrighterScript build takes these files as they are.
