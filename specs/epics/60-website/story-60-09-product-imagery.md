# Story 60.09 — Product imagery: real dark screenshots of every app, in English and Arabic, on demo content

> Epic 60 · Phase 2 · Depends on: 60.01 part B (frames, backdrop, how many per page), 60.02 part B
> (alt text) · Needed by: 60.10, 60.11

## Why

The home and download pages have to show Maktaba: what a household will see on its TV, its phone
and its computer. Drawings or stock photos would promise something the apps do not look like.
Screenshots of the owner's library would publish a person's films and folders. The pictures must
be the real apps, on content anyone may show, in both languages, and easy to take again when the
apps change.

## Description

**What is captured** (each in English and in Arabic, dark):

| App | Screens | Source |
|---|---|---|
| Web | home, a film's page, a series with seasons, search with Arabic and English results, a channel guide | a scratch branch stack (its own Postgres, `HOME`, `MAKTABA_CLUSTER_DIR`, `MAKTABA_ARCHIVE_DIR`) seeded with the demo library; Playwright at 1440×900, device scale 2 |
| Mac app | the library window, the player with subtitles | the Mac app's CI UI tests on `MockHome` (never on a local Mac) |
| iPhone and iPad | home, a film, the player with Arabic subtitles, the Remote | the MockHome UI tests on a per-run simulator, created and deleted by the run |
| Apple TV | home, a series, the player, the live channel guide | `tv-ui-tests` on a per-run tvOS simulator |
| Android TV | home, the player, the guide | Roborazzi references on the demo fixture |
| Android phone | home, a film | Roborazzi references on the demo fixture |

**The demo content** (`tests/fixtures/demo-library/`):
- **Films and series under open licences, with their own artwork:**
  - public-domain documentaries (NASA's and NOAA's films of space, the Earth seen from orbit
    and the deep sea) and Creative Commons ones, with no people in them;
  - children's animation whose characters are all animals (Blender's Big Buck Bunny and the
    Caminandes films), CC BY or CC BY-SA;
  - each title's art is a frame of the film or the film's own openly licensed card.
- **No woman or girl in any picture, title or text:** no poster, thumbnail, still, frame or
  character, and no name or description that implies one; nothing a conservative household
  would not show (no music videos). Each film is checked through, frame by frame on a contact
  sheet, and every picture by eye; a title that cannot be checked is replaced. A test scans the
  titles, synopses and lines for words that name or describe a woman or girl.
- Each title has an English and an Arabic title and synopsis, written for the fixture.
- **No person's name, LAN address or media path:** the household is "The Haddad family", the home
  is «الجهاز الرئيسي» / "Living room", and paths and addresses never show.
- **One fixture for every source:** the same JSON seeds the branch stack, `MockHome` and the
  Android test fixtures, so every app shows the same library.
- **The credits** go on the copyright page (60.07) and in the fixture's README.

**The capture script** (`web/packages/website/scripts/shots.mjs`, plus the jobs' artifacts):
- **Collects** each source's images from its CI run's artifacts (`player-ui-tests-*`,
  `tv-ui-tests`, the Mac run's attachments, Roborazzi outputs, the web capture).
- **Processes** each image:
  - crops to the frame's safe area;
  - strips metadata;
  - writes WebP and AVIF at the breakpoint sizes 60.01 sets (`srcset`);
  - writes `width` and `height` into a manifest;
  - keeps each file under 150 KB at its largest size.
- **Reruns** at any release: `pnpm -C web/packages/website shots --from-run <run id>`. The commit
  that updates images lists them like visual references (60.04's `Reference images:` rule covers
  `static/shots/` too).

**On the page:**
- Each screenshot sits in 60.01's device frame, on the screenshot backdrop.
- A page shows its own language's image.
- **Alt text** comes from 60.02: what the screen shows ("A series page with its seasons and
  episodes").
- The first screenshot above the fold is preloaded and not lazy; the rest are `loading="lazy"`
  with `decoding="async"`.
- Images never mirror in RTL (`data-nomirror`); the Arabic image is a real Arabic screen.

## Acceptance criteria

- **Given** the home and download pages, **then** every screenshot is a real capture of the named
  app on the demo library, dark, in the page's language.
- **Given** any image, **then** it has no metadata, is under 150 KB at its largest size, and has
  its width and height in the page.
- **Given** the demo fixture, **then** it contains only openly licensed titles with credits, and no
  person's name, LAN address or media path appears in any image.
- **Given** a later release, **then** the capture script reruns from that release's CI artifacts
  with no manual step but the review of the changed images.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `static/shots/` | read each file | no EXIF/XMP; size budget; both languages present |
| T02 | unit | the fixture | scan titles, art and licences | every item has a licence and credit |
| T03 | unit | the fixture's strings | scan for names outside the fixture's list, IP addresses and paths | none |
| T04 | build | pages with shots | parse | every `<img>` has width, height, alt in the page's language |
| T05 | Lighthouse | home, download | run | no "properly size images" or "next-gen formats" findings; CLS 0 |
| T06 | script | `shots.mjs --from-run` on a fixture run | run | the manifest and files as listed |

## Edge cases

- **A source whose CI run is red** at the release: the script keeps the previous images for that
  app and says so. A run is never forced green for screenshots.
- **An app screen that changes between releases:** the visual reference check on the home page
  fails, and the person updating looks at both images.
- **Apple TV's focus ring** on the first item: kept, because it is how the TV looks.

## Build plan

| File | Change |
|---|---|
| `tests/fixtures/demo-library/` (JSON, artwork, README with credits) | the demo library |
| `apps/apple/**/MockHome*`, `apps/android/**/fixtures`, the branch-stack seed script | read the shared fixture |
| UI tests for the listed screens (Apple, Android, web capture) | the captures, written as attachments |
| `web/packages/website/scripts/shots.mjs`, `static/shots/`, `shots-manifest.json` | processing and output |
| `.github/workflows/_pr-body-check.yml` | `static/shots/` in the `Reference images:` rule |
