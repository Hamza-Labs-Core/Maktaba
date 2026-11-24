# Story 60.14 — Store gallery images: every app's listing, in every store it ships to, in English and Arabic

> Epic 60 · Phase 2 · Part 1 (design) gates part 2 (the full set) · Depends on: 60.01 part B
> (tokens, the display face, the wordmark), 60.02 (the voice, the tagline), 60.09 (the capture
> pipeline), the new demo library that replaces 60.09's · Needed by: each store submission (an
> owner action)

## Why

Every store a Maktaba app ships to shows a gallery before anyone installs: App Store Connect,
Google Play, Amazon's Fire TV store, Roku, Samsung and LG. The apps reach testers through
TestFlight and Play's internal track today, with no public listing, and the TV store pages
(`docs/stores/`) name screenshots taken for review, not a designed set. A gallery is the
first picture of Maktaba a household sees in a store, so it is customer-facing work and gets
the same design-first treatment as maktaba.tv: one style, reviewed as mockups, before the full
set is made.

Each store sets its own sizes, counts and rules, and some of them conflict with a marketing
style (Google Play refuses device imagery; three TV stores want the app as it is; Samsung and
LG take one set for every language). The sizes are kept in one manifest with each store's own
source, so the set can be checked against it and remade at each release.

## Scope: the listings (checked against the code, 2026-11-23)

| Listing | Store | App, bundle | Built by |
|---|---|---|---|
| `apple-player-iphone` | App Store | Maktaba Player, `com.hamzalabs.maktaba.player` | `testflight.yml` |
| `apple-player-ipad` | App Store | Maktaba Player for iPad, `….player.tablet` | `testflight.yml` |
| `apple-player-mac` | Mac App Store | Maktaba Player for Mac, `….player.mac` | `testflight.yml` |
| `apple-remote-iphone`, `apple-remote-ipad` | App Store | Maktaba Remote, `….remote`, `….remote.tablet` | `testflight.yml` |
| `apple-tv` | App Store (tvOS) | Maktaba TV, `com.hamzalabs.maktaba.tv` | `testflight.yml` |
| `google-player`, `google-remote`, `google-tv` | Google Play | the three Android packages, one listing each for every screen | `play-bundles.yml` |
| `amazon-firetv` | Amazon Appstore | the TV app's `amazon` flavor (the same images serve Vega OS) | `tv-release.yml` |
| `roku` | Roku Streaming Store | the channel | `roku-release.yml` |
| `samsung`, `lg` | Seller Office, Seller Lounge | the Tizen and webOS launchers | `tv-web-release.yml` |

**No images, and why:**
- **Microsoft Store:** after v1 (Epic 13); no MSIX is built.
- **Flathub and the Snap Store:** story 25.29 plans them for the server only; nothing is
  packaged.
- **The server app (Maktaba, Tauri):** a direct download from maktaba.tv, with no store
  listing. The Mac App Store listing is the Player's.
- **The Cast receiver:** has no store listing.

`stores.json` keeps the rules of the unlisted stores for when one ships.

**The size manifest:** `web/packages/website/design/store-gallery/stores.json`. For every
store it records:
- each asset's exact pixel size and the sizes the store also accepts;
- the count, formats, alpha rule and file size limit;
- whether the asset is required;
- the source page on the store's own site, read on 2026-11-23.

Each listing names its sets: which asset, in which style, on which surface, per language or
single. Two of its numbers are weaker than the rest:
- **LG's screenshot counts:** from Seller Lounge User Guide 11.5 (2021). Seller Lounge itself
  needs a login.
- **Apple TV's 1280 × 768 App Store icon:** only on an archived Apple page. The icon comes
  from the build anyway.

The manifest marks LG's numbers `unconfirmed`, and `docs/stores/lg.md` already says to check
them at upload.

## Part 1: the design (this story's first pull request)

**Styles** (`design/store-gallery/README.md` has the measurements):

| Style | What it is | Where |
|---|---|---|
| captioned | the ink field with the lamp's brass glow; a headline in Maktaba Display and one line under it in Maktaba Text, centred; the app's real screen below in a plain rounded panel with a hairline edge, **no device frame**; the first shot of a gallery carries the wordmark in both scripts | App Store (iPhone, iPad, Mac, Apple TV), Google Play (phone, 7" and 10" tablets) |
| plain | the screen exactly as captured, nothing added | Google TV, Fire TV, Roku, Samsung, LG |
| tile | the icon and the name, in the listing's language (both scripts where the set is single) | the Android TV banner, the Fire TV app icon, the Roku poster, Samsung's logo layer |
| feature | the wordmark and the tagline at the inline start, the TV's home screen at the end | Google Play's feature graphic, Amazon's promotional image |
| art | the apps' own library hall (`web/public/art/library/dark-wide-2.jpg`), no text, its lamp placed where the store asks | the Fire TV background (subject right), Samsung's detail background (right 60%), LG's launcher background |
| icon | the app icon, resized | Google Play 512, Amazon 114 and 512, LG 400 |

**Why these choices:**
- **No device frame anywhere:**
  - Google Play refuses device imagery.
  - Apple allows only its own bezels, unmodified.
  - Part B's generic frame is neither, so the screen stands in a plain panel.
- **Captions are measured:** Google Play caps a tagline at 20% of the image. The render
  measures each caption's text area and fails it over 20%, a headline over 2 lines or the
  line under it over 2.
- **The TV stores get plain screens, except Apple:**
  - Google TV needs at least one unaltered screenshot (TV-G4).
  - Amazon asks for "only the app itself".
  - Samsung and LG take one set for every language, so a caption could not be in the
    viewer's language.
  - Apple allows text overlays, so Apple TV is captioned like the other Apple devices.
- **Dark only.** A screen never mirrors: its language is the screen's own. In the Arabic set
  every caption is right to left and every screen is the Arabic app, except the one beat that
  shows the other language.

**The beats** (`beats.json`, each caption written on its own in each language, in the voice of
`copy/voice.md`; Arabic addresses the household in the plural, as the site does):

| App | Beats, in order |
|---|---|
| Player (iPhone, iPad, Android phone and tablets: 7; Mac: 6, no parental controls on the Mac yet) | 1 library (the tagline, with the wordmark) · 2 search what was said · 3 subtitles made at home · 4 every series in order · 5 live channels · 6 Arabic and English · 7 parental controls |
| Remote (iPhone, iPad, Android: 6) | 1 your home at a glance (wordmark) · 2 what needs you · 3 the queue · 4 sign in a TV with a code · 5 it updates itself · 6 Arabic and English |
| TV (Apple TV captioned; the others plain: 7, Roku 6, Samsung 4, LG 6) | 1 library (wordmark) · 2 every series in order · 3 subtitles · 4 live channels · 5 search what was said · 6 sign in with a code · 7 Arabic and English |

- **The language beat:** in the English set it shows the Arabic screen; in the Arabic set it
  shows the English one.
- **Samsung and LG** show English screens, with the Arabic screen second: Samsung hides number
  3 on some TVs.
- **Roku** drops the language beat (6 at most, and each language has its own set).

**Mockups** (`node web/packages/website/design/store-gallery/make.mjs`, then `render.mjs`):
- sheets for the iPhone Player and Remote, the iPad and Mac Player, Apple TV, Google Play
  (feature graphic, icon, phone, 10" tablet) and the TV stores, in both languages;
- four shots at full size: the iPhone library and search shots, the feature graphic, and the
  Apple TV library shot.

Until the new demo library lands, each screen is a **stand-in**: a test-data screen the repo
already has (blank posters, no people), as 60.01 part B used. A beat with no screen yet is a
labelled empty panel.

**The gate:** the style, the beats and the captions are approved before part 2 starts.

**Approved (2026-11-24),** all six decisions as proposed:
1. the captioned style, with no device frame;
2. the beats and the captions above;
3. plain screens for the TV stores, with Apple TV captioned;
4. Samsung and LG as one set for every language: English screens, the Arabic screen second;
5. the full set as a CI artifact in fastlane's layout, not committed;
6. no app preview or promo video.

## Part 2: the full set (after approval, its own pull request)

- **Captures:** every beat on every surface, on the new demo library, dark, in both languages,
  from CI only, on throwaway simulators and emulators. Each `capture` marked `new` in
  `beats.json` is a new UI-test screen:
  - Apple: `MockHome(demo:)` shot tests exported by `tools/apple/export-shots.py`;
  - Android: Roborazzi on the demo fixture;
  - Roku: its screen tests;
  - Samsung and LG: the launcher's Playwright run.

  The sizes are captured natively where a simulator has the store's size: the iPhone 6.3"
  and 6.9", the iPad 13" and Apple TV 4K. Elsewhere the capture is resized into the panel and
  never stretched.
- **Composer:** `design/store-gallery/compose.mjs` (sharp, already a dependency) reads
  `stores.json` and `beats.json` and the runs' artifacts, and draws each image with the same
  `gallery.css` through Playwright. It writes the store's exact size as an opaque PNG or JPEG
  as the store asks, with no metadata.
- **Checks** of every image against the manifest:
  - dimensions and format;
  - no alpha where refused;
  - the file size limit (Samsung 500 KB JPEG, Play 8 MB);
  - the count per set;
  - the caption share.
- **Output:** the workflow `store-gallery.yml` writes an artifact `store-gallery-<version>`, by
  hand or after a stable release, not committed:
  - `apple/<listing>/screenshots/<locale>/NN_<beat>.png` (fastlane `deliver`);
  - `google/<listing>/metadata/android/<locale>/images/…` (fastlane `supply`:
    `phoneScreenshots`, `sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`,
    `featureGraphic.png`, `icon.png`, `tvBanner.png`);
  - `<store>/<listing>/<lang>/…` for Amazon, Roku, Samsung and LG;
  - a contact sheet per listing.

  The repository has no fastlane and uploads stay an owner action, so these layouts are where
  a later upload step expects them.
- **The no-women check:** the pull request lists every image with what it shows. No woman or
  girl appears in any poster, still, frame, avatar, cartoon or name. An uncertain image is
  replaced at the fixture, never edited.

## Acceptance criteria

- **Given** `stores.json`, **then** every listed asset has its size, count, formats and a source
  on the store's own site, and every listing names an asset that exists.
- **Given** a gallery, **then** its count is inside its store's limits, and each beat has a
  headline and a line under it in English and Arabic, with no word the stores refuse (rankings,
  "new", "free", prices, calls to action) and no Latin letters in the Arabic.
- **Given** a captioned shot, **then** its caption's text is at most 20% of the image, the
  headline fits in 2 lines and the line under it in 2, and it does not run into the screen.
- **Given** any image, **then** no woman or girl appears in it, and no stand-in or capture comes
  from the old demo library.
- **Given** the full set, **then** every image matches its manifest entry exactly, and the
  artifact's layout is the one above.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `stores.json` | read every store and listing | every asset has size, count, formats, an https source; every listing's asset exists and its workflow file is in the repo |
| T02 | unit | `beats.json`, `stores.json` | count each gallery | inside the store's count (Apple 1–10, Play phone 2–8, tablets 4–8, Roku 0–6, Samsung 4) |
| T03 | unit | `beats.json` | scan the captions | both languages; no refused word; no Latin letters in the Arabic; a headline at most 52 characters |
| T04 | unit | the stand-ins | resolve each path | the file exists; none is from a `demo/` reference or a maktaba.dev screenshot of the old library |
| T05 | render | the mockups | `render.mjs` | caption share at most 20%, headline at most 2 lines, both faces and every picture loaded; each PNG under 1 MB with no metadata |
| T06 | unit | `make.mjs` | `--check` | the committed mockups are current |
| T07 | CI (part 2) | a run's artifacts | `compose.mjs` | every image's dimensions, format, alpha, bytes and count match `stores.json` |
| T08 | review (part 2) | the artifact's contact sheets | look at each image | the no-women checklist in the pull request, one line per image |

T01–T06 run in `make test-tokens` (the `unit_tokens` job), with 60.01's logical-property lint
over the gallery's CSS and mockups.

## Edge cases

- **A store changes its sizes:** the manifest's date and sources say when it was read; the
  composer fails on a size the manifest does not hold, and the manifest is read again.
- **iPhone Duo:** optional now, required from April 2027 for apps built with the iOS 27.1 SDK.
  The Player and Remote run on it, so the set gains the Duo inner size once a Duo simulator
  can capture it.
- **A beat a platform cannot show yet** (parental controls on the Mac: Epic 57 leaves the
  Mac to a later epic) is left out of that surface's gallery (`only`), never drawn.
- **Arabic on a single-set store:** Samsung and LG show the Arabic screen second, and their
  listing text carries both languages.
- **App previews and promo videos:** optional in every store. None is made in this story.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/design/store-gallery/stores.json` | the size manifest, with sources |
| `…/store-gallery/beats.json` | the beats, the captions in both languages, the captures and stand-ins |
| `…/store-gallery/gallery.css`, `sheet.css`, `make.mjs`, `render.mjs`, `mockups/`, `png/`, `README.md` | the style, the mockups and their renders |
| `…/store-gallery/tests/store-gallery.test.mjs`, `Makefile` (`test-tokens`), `.github/ci-paths.yml` (`unit_tokens`) | T01–T06 |
| part 2: `…/store-gallery/compose.mjs`, `.github/workflows/store-gallery.yml`, new UI-test screens in `apps/apple`, `apps/android`, `apps/tv` | the full set |
