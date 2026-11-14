---
schema: 1
kind: pr
pr: 1240
title: "A TV signs in from anywhere with a code through Maktaba Cloud (58.03, the API)"
date: 2026-11-14
session: d117b787-a5b7-4ff7-8b90-0a087d3c82d8
agent: claude-code
---

## What happened
- #1240 merged as f614dcf16. The cloud serves `/v1/tv/codes` (create, poll) to a TV with no
  account, codes from a 30-letter alphabet (no 0, 1, I, L, O, U) that last 10 minutes, at most 10
  per address, swept an hour after they expire (migration `00580001_tv_codes.sql`). The home reads
  and claims a code through its relay agent (`cloudlink.TVCodesHandler` on the loopback admin
  port), capped per home at a burst of 30 then one every 6 s, counted after the secret check.
- On the home, `GET /api/pairing/cloud-code/{code}` and `POST /api/pairing/cloud-approve` let a
  signed-in person approve; the home hands the cloud its connection list and a single-use grant,
  sealed and delivered to the TV's poll once (then 410, holding no secret), and the TV redeems
  the grant at `POST /api/pairing/device/token`. `GET`/`DELETE /api/me/app-sign-ins` list and
  sign out a person's code-signed apps. A `tv-code` scenario in the cloud rehearsal proves the
  whole path through a real relay; `TestTVCodesOnTheRealRouter` proves it on the real router.
- Security reviews during the work hardened the design before merge: a cloud code can be read
  out to anyone, so its TV is a restricted sign-in (JWT claim `rst`, kept at refresh through
  the refresh family's first token): never an admin even for an admin's approval (every library
  listed instead), and it mints nothing (`principal.MintsNoCredential`: no API token, no
  approval of a home or cloud code, no pending list). The approval ticket's prefix
  (`device-cc.`) carries the cloud origin, so a restart between approval and redeem still mints
  a restricted sign-in.
- Main moved under the pull request many times; it was merged in with `tools/dev/merge-main.sh`
  each time, with conflicts across the rounds in the cloud rehearsal workflow, `p10.go`,
  `simhome.go`, `homes.go` (main's 50.16 relay hint), `problems.json`, `openapi.ar.json`, the wiki JSON and
  `links-baseline.json` (main's Roku channel named the same unbuilt `maktaba.tv/tv` page).

## What we learned
- GitHub reports this repository's CI checks without a `ci / ` prefix (`unit / unit`,
  `affected-tests`, `website / website`). A merge watcher given the prefixed names waits
  forever on a green pull request; one ran out its two hours that way.
- A pull request that conflicts with main runs no `pull_request` workflows, so a watcher on a
  conflicting head can never see its checks: check `mergeable` before arming one.
- Structured files that both sides append to (`links-baseline.json`, `problems.json`,
  `openapi.ar.json`) merge cleanly by reading the two sides (`git show :2:` and `:3:`) as data
  and writing the union, not by editing conflict markers.
- `ScratchDB` tests skip silently when `MAKTABA_TEST_PG_URL` is unset; a local run that passes
  without it proved nothing.
- The devportal's tests read its built output: a stale local `dist` fails three of them until
  `npm run build` runs.

## Keep doing
- Read the required check names from the pull request's own rollup before arming a watcher.
- Answer a security review with a test that fails on the old behaviour (an admin's cloud-code
  TV refreshing as a viewer, a restricted sign-in approving nothing, approval surviving a
  restart).

## Stop doing
- Copying a watcher's check list from an earlier session or workflow layout.

## Open
- The approve screens in the Android and Apple Players and the web, and app sign-ins in
  Sessions, are #1250.
- The `maktaba.tv/tv` approve page (`web/packages/cloud-web`), with approval from the web for a
  linked account, ships next; it clears the six `maktaba.tv/tv` entries in
  `links-baseline.json` (`--prune-baseline`). The Roku channel's `qr.png` follow-up waits on it.
