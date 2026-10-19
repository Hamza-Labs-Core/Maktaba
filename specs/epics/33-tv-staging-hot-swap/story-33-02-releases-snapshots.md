# Story 33.2 — TV releases and snapshots

## Description

*As the owner, I want every version of my TV — the streaming software, its encoder settings, the
lineup and the schedules — to be one named, frozen thing, so I can stage it, compare it and go back
to it.*

A **TV release** is immutable: the streaming binary version and checksum, a snapshot of
`rules.tv` / `rules.tv_output`, a snapshot of every channel definition and its art, and the schedule
generation it reads. A slot is bound to one release for its whole life.

Design: [design.md](design.md) §3.

## Acceptance criteria

- **AC1** `tv_releases` and `tv_release_channels` exist (slot 0123); a release has a digest over its
  snapshot; building a release equal to an existing one returns the existing one.
- **AC2** Release 1 is created at first start (or migration bootstrap) from `channels`, `app_settings`
  and the running version; `tv_routing.cluster.live_release_id` = release 1, `live_gen` = 0.
- **AC3** A slot started with `MAKTABA_TV_RELEASE=R` reads channel definitions from
  `tv_release_channels(R)`, settings from `tv_releases.settings` and programmes of
  `tv_releases.sched_gen` only; editing `channels` or `app_settings` does not change what a running
  slot airs.
- **AC4** Release binaries are installed in `bin/releases/<version>/`; the supervisor refuses to start a
  slot whose binary's SHA-256 differs from the release (`tv_slot_refused: checksum`).
- **AC5** Art in a snapshot is referenced by content digest; replacing a channel logo creates a new file
  and leaves the old one for the old release.
- **AC6** `GET /api/tv/releases` lists releases with kinds, version, generation, digest and the change
  that produced them.
- **AC7** Releases are never edited; deleting is GC only (story 33.3 §GC rules).

## Test cases

- **TC1** `test_release_digest_stable` — same inputs, same digest; order-independent.
- **TC2** `test_release_bootstrap_from_live`.
- **TC3** `TestSlotRepoReadsRelease` — `channels` changed after start, slot unaffected.
- **TC4** `TestSupervisorChecksumRefusal`.
- **TC5** `test_art_content_addressed`.

## Edge cases

- **EC1** A channel deleted from `channels` by hand while a release references it: the slot keeps
  airing it from the snapshot; the next release drops it; programmes FK uses `lifecycle` (33.3), so
  hand deletes are blocked while referenced (`ON DELETE RESTRICT` from `tv_release_channels`).
- **EC2** A binary version already removed by GC: staging a release needing it re-downloads it (33.14)
  or fails with `binary_missing`.

## Dependencies

33.1.
