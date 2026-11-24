---
schema: 1
kind: pr
pr: 1599
title: "Homes find releases in Maktaba's own signed release feed on maktaba.tv, not in GitHub's list of every release"
date: 2026-11-24
session: c5f717c4-2731-403e-be00-cb29f0302e66
agent: claude-code
---

## What happened
- **Why.** GitHub's list of releases reached 10.5 MB for 30 releases on 2026-11-23
  (d20261124-stale-catalog-step-down). GitHub can't filter it by platform; #1592 read it in pages
  as a stopgap. A decision on 2026-11-24 replaced it with Maktaba's own feed.
- **The feed.** `https://maktaba.tv/updates/v1/<channel>/<platform>.json`, with a `.sigstore.json`
  beside each file, and `<channel>/index.json`.
  - It lists the newest 10 releases, each with only its platform's files, plus digests, the
    migration set and pulled releases marked `withdrawn`.
  - About 25 KB raw, about 4 KB gzipped, built from the real v0.3.161 (190 files on GitHub).
  - Code: `shared/go/releaseverify/feed.go`.
- **Signing.** Keyless, by `update-feed.yml` on main: triggers `workflow_run`, `workflow_dispatch`
  or `schedule`, on a GitHub-hosted runner, with the repository's ids. `VerifyFeed` reuses the
  release's bundle check (`verifyBundle`), then checks that the content is exactly what was asked
  for, with every file at its release's own GitHub address. No new key and no secret.
- **Hosting.** maktaba.tv's existing Worker.
  - The feed is the `update-feed` artifact; every site deploy carries the newest one
    (`tools/release/website-feed.sh`), or the live feed when there is none.
  - The Epic 25 `releases.maktaba.tv`/`updates.maktaba.tv` plans were never built.
- **Home side.** The catalog (per platform of every machine), the pre-run re-check, the About
  check and `maktaba-server update` read the feed first.
  - GitHub's paged list is read only when the feed is unreachable or older than the last one read.
  - A refused feed stops the check.
  - The mirror serves any signed file from beside `checksums.txt`, so packs still mirror.
- **The live check.** `update-feed.yml`'s `live` job fails unless maktaba.tv lists the new release
  for every platform, verified as a home verifies it.
- **Pulls.** `tools/release/pull-release.sh` sets the repository variable `UPDATE_FEED_WITHDRAWN`
  and re-runs the feed.
- **Rehearsal.** The update rehearsal serves the feed at its real shape, and this commit's home
  must find each `next` release in it.
- **Overlap.** #1597 (silent update-check failures) touches the same files. Its session was
  messaged with the exact overlap, and whichever PR lands second merges main in.

## What we learned
- **Rollbacks.** A rollback of maktaba.tv serves an older signed feed. Treating "older than the
  last read" as a refusal would stop every home's updates after a site rollback. It falls back to
  GitHub instead. A pull a home already saw stays withdrawn in its catalog, so a replay gains
  nothing.
- **Platform filtering.** A home installs for its cluster, not only itself. A feed limited to the
  home's platform would starve its machines and the mirror's packs. So the home reads each
  machine's platform feed, and the mirror derives a signed file's address from `checksums.txt`'s.
- **Fakes in tests.** Test and e2e builds must never default to the real feed: a fake Sigstore's
  releases would meet maktaba.tv's real signature and be refused. A test's API base, a fork and
  e2e builds without `MAKTABA_E2E_UPDATE_FEED` all read no feed.
- **The fixture.** A recorded release's three signed files still verify against the embedded
  root. So the generator's tests verify the real v0.3.161, not a fake.

## Keep doing
- Record a real release (`gh release view` plus its signed files) as the fixture for anything
  that reads release metadata, and measure sizes from it.
- Verify what a workflow signs with the homes' own code before publishing it.

## Stop doing
- Reading an unbounded, unfilterable third-party list on every home when a small file we sign
  and host can say the same.

## Open
- The first live feed appears after the next release once #1599 merges (or a manual
  `update-feed.yml` run). Until then homes on that code fall back to GitHub.
- A website deploy held by the nightly sweep gate also holds the feed back. The live check
  reports it, but nothing deploys the feed alone.
- Homes on v0.3.153–v0.3.161 learn of a pull only from GitHub. Mark a pulled release a
  pre-release there too while any remain.
