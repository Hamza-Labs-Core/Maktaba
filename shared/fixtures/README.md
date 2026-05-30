# shared/fixtures — canonical test fixtures (Story 20.2)

This directory is the single source of test fixtures shared across
services (Story 20.2 / HLB-389). Before this landed, `shared/fixtures/`
did not exist, which the Epic 20 gap analysis flagged as the linchpin
dependency for integration (20.4) and e2e (20.5).

## What is here now (delivered)

| Path | Purpose | Story 20.2 ref |
|---|---|---|
| `LICENSE` | Provenance/licence statement for every committed sample. | AC4 |
| `probe-goldens/no-audio.probe.json` | Video with no audio track. | EC1 |
| `probe-goldens/corrupt-moov.probe.json` | Unreadable moov atom → `probed:false`. | EC2 |
| `probe-goldens/rtl-filename.probe.json` | Arabic RTL filename round-trip. | EC3 |
| `samples/` | Reserved for real media (see DEFERRED). | AC1 |

The `probe-goldens/*.json` files describe the expected probe outcome
for the three Story 20.2 edge cases. The field names mirror
`streaming/internal/probe.Row`. They are pure JSON (no media bytes, no
licence encumbrance) and are directly consumable by table-driven Go
tests and pytest parametrize today — a contract a downstream epic can
assert against without needing the multi-gigabyte media first.

The `_fixture` key in each JSON documents the invariant under test and
is ignored by consumers (any unknown key is).

## DEFERRED (explicit — not silently dropped)

The following Story 20.2 items are **deliberately not delivered here**
and remain open. They are large, binary, licence-encumbered, and/or
under-specified, and — critically — **no existing test consumes them**,
so a partial/synthetic delivery would add weight without value:

- **AC1 real media samples** (`samples/{ar,en,mixed,multitrack,4k}`):
  require real Arabic/English/mixed/multitrack/4K-HDR recordings with
  per-file licences. Synthetic media cannot produce trustworthy
  probe/transcript goldens, so faking them would be a false fixture.
  When added, each MUST get a `samples/<name>.sha256` and a stanza in
  `LICENSE` (the contract is already written there).
- **AC2 size guard + 4K download-on-demand** (`scripts/fixtures-make.sh`,
  `4k-hdr.manifest.json`, `fixtures-check.sh` ≤50 MiB gate): depends on
  the media existing first.
- **AC3 `seeded_db`** (1k videos / 10k segments, `seed-db.sh` /
  `generate-seeded-db.go`, ≤5s restore): a generator + checked-in
  compressed dump; depends on the migration schema being frozen and is
  Story-20.4 (integration) infrastructure, not a leaf fixture.
- **AC1 transcript/probe goldens for real media**: derived from the
  deferred media; cannot precede it.

Tracking: Epic 20 Story 20.2 residual (HLB-389). Raising the
`probe-goldens` set or adding `samples/` real media are the next
concrete steps; each must arrive with its `LICENSE` stanza.
