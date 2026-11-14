---
schema: 1
kind: pr
pr: 1307
title: "Epic 58 story 58.12: the Roku SceneGraph channel"
date: 2026-11-14
session: fe74e94d-9f17-4608-8868-2c6bcb57fc86
agent: claude-code
---

## What happened
- #1307 merged as 0897be885. `apps/tv/roku` is now a channel:
  - sign in by a cloud code (58.03's API), by the home's own code after the /24 probe, or by
    the home's address; the next launch opens the saved home;
  - Home, the libraries with five tabs, Play and Shuffle, the letter rail and cursor paging
    (three pages of posters at most), series, search, Resume or Start over, and the account page;
  - deep links per media type; Back's steps and the leave dialog;
  - English and Arabic, every screen mirrored.
- The PR adds:
  - the build (`tools/build.mjs`: BrighterScript + bslint, the bundled IBM Plex Sans Arabic,
    icons and splash drawn from the Maktaba mark, zip under 4 MB);
  - a device-test package (`--device-tests`) that runs the same BrightScript logic tests on a
    Roku in place of Rooibos (story T04 updated);
  - `tests/screens.test.mjs`: the channel on brs-node's SceneGraph against a mock home and
    cloud, keys over ECP, focus read from the channel's log, and 16 reference screenshots.
- The `roku / Roku channel` job runs all of it in about 2.5 minutes, including Find on this
  network (Linux only). `docs/stores/roku.md` lists the owner's steps (account, `genkey`,
  packaging, dashboard) and new real-device rows. The devportal TV page lists Roku.
- Three CI failures on the way:
  - `maktaba.tv links`: the code screen names maktaba.tv/tv, 58.03's page that is not live yet;
    five catalog files went into the links baseline with story 58.03;
  - Prettier on the devportal script;
  - two merges of main that conflicted in `apps.mjs` (webOS, then Tizen, each added a TV
    platform row in the same places).

## What we learned
- brs-node draws text through node-canvas (Pango), which runs the bidi algorithm. Roku never
  does, so 58.15's pre-reordered Arabic came out back to front in every screenshot.
  `tests/roku_text.cjs`, preloaded with NODE_OPTIONS, wraps each string in LRO … PDF. The
  emulator then draws in stored order, and the channel stays unchanged.
- brs-engine's RowList declares `rowTitleComponentName` but never draws it. It ignores
  `jumpToRowItem` for an unfocused row, and in scroll mode it draws only from the offset
  forward. The portable RTL recipe:
  - measure each title with a Label and set a per-row `rowLabelOffset`;
  - pad short rows at the logical end with blank cards that a focus observer skips;
  - use `floatingFocus` in Arabic.
  - Unfocused rows that open from their far end are a real-device row, not a channel bug.
- `ui_Mirror` needs a grid's real width: PosterGrid and MarkupGrid report one item's size,
  so the width is columns × item + spacing. Without it the Arabic grid sat off screen.
- brs-engine's roUrlTransfer is synchronous. A probe of 253 addresses only finishes where they
  refuse at once (Linux 127.0.0.x). macOS has no such aliases, so the Find test is skipped
  there. A `node:24-bookworm` container with its own node_modules volume runs it locally.
- `node --test` runs files at the same time. Builds share nothing only when each package
  stages into its own folder. bsconfig's `stagingDir` beats the command line, so the build
  writes a small extending config per package.
- macOS's case-insensitive match made the root `.gitignore`'s `MANIFEST` hide Roku's
  `manifest`. `apps/tv/roku/.gitignore` un-ignores it.

## Keep doing
- Look at every screenshot, both languages, before writing references: all tests passed
  while Arabic text was reversed and the grid was off screen.
- Run a Linux-only test in a container before the first CI run.

## Stop doing
- Hand-editing a long devportal row without running Prettier on `web/` before the push.

## Open
- `GET /v1/tv/codes/{code}/qr.png` waits for #1240 (58.03's `tvcodes` package). The handler
  is drafted on `rsc.io/qr`: public, 404 for unknown, expired, claimed or malformed codes, and
  encoding only the verify URL. With it comes a `docs/stores/roku.md` row: a TV signed in by a
  cloud code an admin approved shows no admin-only screen and cannot approve another device.
- The links-baseline entries for maktaba.tv/tv go once 58.03's page is live (`--prune-baseline`).
- Real-device rows 1–18 in `docs/stores/roku.md`, and 58.13's player and certification.
