# Roku

The Roku channel (stories 58.12 and 58.13), in `apps/tv/roku`: BrightScript and SceneGraph,
English and Arabic (the whole channel mirrored, Arabic shaped as story 58.15 says). Story 58.04
adds the submission notes here; story 58.13 adds the player, the listing, the certification
table and the release.

What it does not do: nothing is sold in it (no Roku Pay, no purchase links), and it holds no
catalog feed: a home's titles stay in the home, so the channel is not in Roku's search results.

## What the owner does

The store account, the signing key and each submission are the owner's; nothing in the
repository or in CI holds them.

1. **A Roku developer account** (developer.roku.com, enrolled once, no fee), and a Roku in
   developer mode for the checklist (Home ×3, Up ×2, Right, Left, Right, Left, Right on the
   remote; it asks for a password for the developer web page, kept by the owner).
2. **The signing key**, made once on one Roku and kept off the repository: telnet to the device's
   port 8080, run `genkey`, and keep the DevID and the key's password it prints (in the owner's
   password manager). The same key signs every version: the dashboard refuses a package signed by
   another key, and a lost key means a new channel.
3. **Each package**: a `v*` tag whose channel changed attaches `maktaba-roku-<version>.zip` and
   its `.sha256` to the tag's release (`.github/workflows/roku-release.yml`; the manifest's
   `build_version` is minor × 1000 + patch, so each package is above the last). Check the
   checksum, side-load the zip on the keyed Roku from its developer web page (Installer), then
   Utilities → Packager with the key's password. The page gives the signed `.pkg`. Roku packages
   only on a device, so no workflow signs it.
4. **A beta channel first** (up to 20 testers, the dashboard's Beta channels): each release
   candidate's `.pkg` goes there, the real-device checklist below runs on it, and its result
   line is written under Results.
5. **The dashboard**: the public channel (no ads, no purchases: Roku Pay, RAF and app-ads.txt
   do not apply), a new version with the `.pkg`, the listing (below), the review home's
   credentials (below), the deep-link sample IDs (below), and the checklist's passed result for
   that version, then **Submit for review**. Roku's certification runs the table below.
6. **After approval**, Roku updates installed channels on its own; the compatibility contract
   (story 58.01) covers a home released before Roku's approval of the matching channel.

## Listing

- **Name:** Maktaba. **Category:** Movies & TV (personal media). **Price:** free; nothing is
  sold in the channel.
- **Short description (en):** Watch your own Maktaba home library on your TV, in English or
  Arabic.
- **Short description (ar):** شاهد مكتبتك المنزلية في مكتبة على التلفاز، بالعربية أو
  الإنجليزية.
- **Long description:** what the channel does (sign in to a Maktaba home by a code, browse its
  libraries, play with subtitles and Up next), that it needs a Maktaba home, and that it holds no
  catalog of its own. Screenshots: the channel's screen tests' references
  (`apps/tv/roku/tests/screens/`, dark, test data), taken again on the keyed Roku at 1920×1080
  for the dashboard.
- **Languages:** English, Arabic. **Countries:** every country Roku offers public channels in.

## Certification

Roku's certification criteria as of 2026-04 (research.md §6.5), and how the channel meets each.
A check named T03 or T05 is a test of story 58.13; a row number is the checklist's below.

| Criterion | How Maktaba meets it | Check |
|---|---|---|
| Home screen in 15 s or less | lazy rows, small posters; `AppLaunchComplete` signalled when Home is drawn | the screen tests' launch budget (`home-drawn-ms.txt`), row 3 |
| Screens in 3 s or less; playback in 8 s or less | the session opens as the player draws; direct play first | rows 3, 19 |
| Deep linking for each media type | 58.12's router; sample IDs below | T03 (`tests/player_test.brs`, the screen tests), row 15 |
| Voice: direct to play | the input event with a content ID takes the deep-link path | T03, row 11 |
| Back exits from Home, with one confirmation at most | 58.12's leave dialog | the screen tests, row 16 |
| No ads | the channel has no ads: RAF and app-ads.txt do not apply | none |
| Instant Resume | required only above 5M US streaming hours a month; not built | none |
| Accessibility | captions (Roku's renderer, and the channel's own layer for Arabic); the audio guide reads labels | rows 8, 12 |

**Deep-link sample IDs** for the dashboard: one video of each media type (`movie`, `episode`,
`series`, `shortFormVideo`) from the review home (story 58.04), read from its libraries when
the submission is made; the review home's seed keeps the same videos from one round to the next.

**Roku Automated Channel Testing** (T05): Roku's WebDriver needs a Roku on the runner's network,
so it runs on the owner's Roku from a laptop on the same network (checklist row 2), not in CI:
launch to Home with the timing, play a video, Back out, keeping the debug console (port 8085)
with the result.

## Build

- `node tools/build.mjs` (in `apps/tv/roku`) compiles the channel with BrighterScript and
  bslint (any error fails it), adds the bundled IBM Plex Sans Arabic (Roku's system font has no
  Arabic) and the icons and splash drawn from the Maktaba mark, and writes
  `build/maktaba-roku.zip`, failing over Roku's 4 MB limit (about 0.5 MB today).
- `node tools/build.mjs --device-tests` writes `build/maktaba-roku-device-tests.zip`: the
  channel's source with its logic tests (the files brs-engine runs in CI) in place of its Main.
  Side-loaded, it prints one `RESULT pass=<n> fail=<n>` line per test file to the debug console
  and `[device-tests] done` at the end (checklist row 1).
- The manifest's version is the channel's own (`major_version`, `minor_version`,
  `build_version`); a release raises it before packaging.

## Submission notes

The store's reviewers sign in to the project's review home, review.maktaba.tv (story 58.04).
The notes, what reviewers get and how a submission round goes are in
[review-home.md](review-home.md): its text goes in the Developer Dashboard's **Channel credentials** (the test account) and the notes for certification. The password is never written
here: the notes name the release secret `REVIEW_HOME_VIEWER_PASSWORD` (and, for the parental
checks, `REVIEW_HOME_CHILD_PASSWORD`), and the release session fills each in when it submits.
After the round, the release checklist rotates both ([review-home.md](review-home.md#a-submission-round)).

## Real-device checklist

Run before each channel release candidate is uploaded to the Roku dashboard (58.12 T04–T05,
58.13 T05–T06). The rule and the result line are in [README.md](README.md).

**What CI covers instead:** the BrighterScript build and lint, and brs-engine logic, focus,
layout and render tests (brs-engine screenshots are not Roku's renderer). Roku has no emulator.
**Only a device shows:** the Video node, rendering, fonts, timing, certification.

### Devices

- Roku Express (HD)
- Roku Express 4K
- Roku Ultra
- a Roku TV

Each in developer mode, the candidate side-loaded from the developer web page. Capture the
debug console (`telnet <roku-ip> 8085`) for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | The logic tests on the device: side-load `maktaba-roku-device-tests.zip` | every `RESULT` line says `fail=0`, then `[device-tests] done` |
| 2 | Roku Automated Channel Testing (WebDriver): sign in by code against the review home, browse, a deep link, Back out | pass; the 8085 console kept with the result |
| 3 | Launch to Home on the Roku Express | within Roku's limit (15 s); screens within 3 s; playback within 8 s |
| 4 | Formats: H.264, HEVC, a file the home remuxes or transcodes | each plays |
| 5 | HDR10, and Dolby Vision profile 8 (played as HDR10) on the Ultra and the TV | HDR on the TV |
| 6 | EAC3 audio on a receiver or soundbar | passthrough |
| 7 | Switch to Arabic | text joined and in reading order, rows start at the right, Left moves forward |
| 8 | Arabic subtitles and the player overlay | drawn joined, right to left, in the bundled font |
| 9 | A Persian title with letters outside the shaping table | shown in logical order, logged, no crash |
| 10 | Shaping 20 long descriptions on the Roku Express | within 50 ms per screen |
| 11 | Voice search | the query reaches the home and results show |
| 12 | The audio guide | reads every focusable label |
| 13 | Posters on Home, a library's grid and a series | every poster draws (the scene's HTTP agent carries the bearer; brs-engine fetches posters without it, so only a device shows this) |
| 14 | A home reached by its direct HTTPS name (`1-2-3-4.<home>.…`), and the relay | Home loads over TLS with the device's certificate bundle |
| 15 | Deep links from the developer web page's ECP (`curl -d '' "http://<roku-ip>:8060/launch/dev?contentId=<id>&mediaType=movie"`), for `movie`, `episode`, `series` and `shortFormVideo`, at launch and while running | each plays; an id the viewer cannot watch opens Home with the not-available line |
| 16 | Back from the first screen | the leave dialog shows, Stay keeps the channel, Leave exits |
| 17 | Down a library of a few hundred videos on the Roku Express, then back up | no out-of-memory exit; the 8085 console shows at most three pages of posters loaded |
| 18 | Arabic: every row of Home, not only the focused one, opens on its first card at the right; row titles sit at the right | as said (brs-engine shows unfocused rows from their other end) |
| 19 | The player (58.13): a 4K HEVC HDR10 MKV with EAC3 on the Ultra | direct play (the 8085 console's `[player] direct mkv`), HDR on the TV, EAC3 passed through |
| 20 | An AV1 source on the Ultra (2024), and on a Roku without AV1 | the Ultra plays DASH (`[player] remux dash`); the other gets a transcode (`[player] transcode hls`) |
| 21 | A Dolby Vision profile 8.1 file on a Dolby Vision Roku | plays as HDR10 |
| 22 | A TrueHD file | plays with EAC3 (and a stereo AAC track beside it) |
| 23 | The 1080p Roku Express on a 4K source | the home transcodes (`device_caps` says 1080p H.264 only) and it plays |
| 24 | Arabic subtitles, then English | Arabic drawn by the channel joined and right to left in the correct word order; English drawn by Roku, following Roku's caption settings |
| 25 | Roku's captions setting on, no subtitle chosen in Maktaba | the subtitle in the channel's language shows, if the video has one |
| 26 | The overlay: Left and Right seek with the trickplay thumbnail (mirrored in Arabic), Play pauses, the audio picker switches tracks | as said |
| 27 | A queue (Play all, Shuffle): Up next | shows 15 s before the end; OK plays the next; the queue moves on its own at the end |
| 28 | Progress | the home's Continue watching shows the position after 10 s of play, a pause, and Back |
| 29 | A stream that fails on the Roku (a file the Video node refuses) | one retry with a converted version (Trying a converted version…), then the error with Back |

## Results

No result yet (the first release candidate records the first line).
