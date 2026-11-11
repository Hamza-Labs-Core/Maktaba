---
schema: 1
kind: pr
pr: 1137
title: "Epic 59.14: NAS guides on maktaba.tv (en+ar), the strings pass, the Arabic build check"
date: 2026-11-11
session: 6a01f297-be98-4701-b3a6-250b16ecb4d9
---

## What happened
- #1137 (merged at d0969b5b2) finished story 59.14. maktaba.tv's install site
  (`web/packages/install`) gained `/install/nas` (which guide, a model table from
  `src/nas-models.json` saying Fits / Fits with more memory / Use as storage, what to prepare),
  guides for Unraid, QNAP (marked untested on a device) and UGREEN/ASUSTOR, After setup and
  Troubleshooting (one anchored section per message), each with its Arabic twin under `/ar/`.
- The install build fails on an Arabic page over 20% Latin, through maktaba.dev's `parity.mjs`
  (`src/latin.ts`). The model table and NAS menu names kept in English in «quotes» count as
  names. `twins.test.ts` keeps every pair mirrored; the scripted pages (docker, synology) are
  checked on what their script renders.
- `web/src/i18n/epic59.test.ts` (T01) reads every Epic 59 story's Strings table and fails on a
  key missing from either catalog; `container.*` keys are checked in the server's pair, and
  `container_texts_test.go` (T03) holds each container message to two lines, English then Arabic.
- `web-ui.yml`'s new `install` job runs Playwright (T04: the install page's compose equals the
  generator's and no request carries an answer; T05: every guide RTL and mirrored in Arabic,
  dark, photographed in both languages as the `install-guides-screenshots` artifact).
- maktaba.dev gained `docs/developers/contributing/nas.md` and its Arabic: the NAS architecture map.
- Problems found and fixed on the way:
  - The Synology guide's step 5 (#1120) told households to finish setup in the browser at
    :8088, but the container serves nothing until `container setup` runs (`waitSetup`). Step 5
    (en+ar) and `docs/nas/checklists/synology.md` now say Container Manager's terminal.
  - The container's "not set up yet" line was English only; it is now English then Arabic, in
    each NAS's words (`MAKTABA_PLATFORM`: TrueNAS Shell, DSM terminal, Unraid Console).
  - #1138's older-image container line was English only; it now prints the Arabic after it.
  - The first CI round failed in ten jobs from one cause: `epic59.test.ts` imported `node:fs`,
    and the web build's `tsc` (also run by the web image, the Linux payload and e2e) has no node
    types. It now loads `node:fs` at run time, as `help.coverage.test.ts` does.
  - affected-tests flagged a test as stale when the CPU names it used moved into JSON; the test
    now reads them from the data.
  - Moving #1138's older-image line into the bilingual table broke its update-leg test
    (`tests/nas/test_update_leg.py` parsed `cmd_container.go` for the English format), so `plan`
    and then `nas-shapes` failed; the test now reads the line from `container_preflight.go`.
  - `integration` failed once in `pgbundle`'s `TestRealRestartAfterAKillWithItsPIDReused` ("the
    killed postmaster's children still hold its shared memory"); this PR touches no `pgbundle`
    file, and the re-run on the next head was the one allowed.
- The strings check followed the parallel stories as they landed: 59.12 (#1138) came off the
  pending list with three keys mapped to the Updates page's names, 59.13 (#1140) with its keys
  as the spec names them, and 59.16 (#1142, merged at c5b5f79c5) last, once
  `library.not_available_here` was in both catalogs. The check now covers every Epic 59 story
  with nothing pending.

## What we learned
- Before writing a guide's setup step, read what the container does before it is set up
  (`cmd_container.go`'s `waitSetup` and `setupHint`): a wrong step shipped once in #1120.
- A web test that reads files outside `web/` must not import node modules at the top: the web
  build typechecks tests and the web image builds from `web/` alone.
- An implementation may rename a spec's string keys (59.12 used the Updates page's prefixes);
  the strings check maps them in one documented table rather than failing or editing the spec.

## Keep doing
- Run `pnpm -C web exec tsc -p tsconfig.json` after adding any test file under `web/src`.
- Trial-merge a parallel story's branch in a scratch worktree before it lands, to see its
  conflicts and its strings early.
- When text moves (a log line into a bilingual table), grep the repo for tests that read it in
  its old place, including the Python harnesses under `tests/nas`.

## Stop doing
- Writing guide steps from another guide's wording without checking the code path.

## Open
- NAS screenshots to take on the devices (dark, en+ar): `screenshots/unraid/{template,console}`,
  `screenshots/qnap/create-application`, `screenshots/ugreen/project`, beside TrueNAS's and
  Synology's.
- Publishing `web/dist-install/` at maktaba.tv's `/install/` and `/ar/install/` is a maintainer's
  action; the menu names on an Arabic DSM/QTS are still to check on a device.
- The QNAP guide is untested on a device until the checklist covers one.
