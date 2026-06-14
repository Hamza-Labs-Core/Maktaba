# Plan 27.2 — Program scheduler — implementation

> Implementation plan for [story-27-02-program-scheduler.md](story-27-02-program-scheduler.md).
> Self-contained. Cross-links: reads `channels` (slot 0081,
> [Plan 27.1](plan-27-01-channel-definition.md)), the smart-query
> evaluator (Story 7.14), `series_episodes` (slot 0075,
> [26.3](../26-content-intelligence/plan-26-03-series-detection.md)), and
> `video_classification`/`video_topics` (slots 0074/0046) for smart-mix;
> draws filler from slot 0085 ([Plan 27.10](plan-27-10-filler-bumper-system.md)).
> Consumed by the live engine ([Plan 27.3](plan-27-03-live-stream-engine.md))
> and all guide surfaces ([Plan 27.4](plan-27-04-epg-generation.md)).
> Writes slot 0082 (`channel_programs`, `channel_schedule_state`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Scheduler is a Python pipeline module**, run as a debounced library pass + a periodic horizon top-up (cron), not request-time. | Smart-mix needs Epic 26 classification (Python); 48 h planning is batch work; mirrors the 26.4 auto-collection pass model. |
| D2 | **Blocks carry absolute `start_at`/`end_at` (UTC).** The timeline is contiguous: `prev.end_at == next.start_at`. | Wall-clock anchoring is the whole epic (README); clock-sync, cheap guide, cold-channel "now playing" all derive from it. |
| D3 | **Past + current blocks are immutable; regen only rewrites the future tail** from the next boundary after `now`. | Story AC2 — rewriting the current block jumps live viewers. |
| D4 | **Per-mode planner behind a common `Planner` interface**; each yields `(video_id, source_offset, source_duration)` items the packer lays onto the timeline. | Isolates mode logic; the packer (padding, boundary alignment, contiguity) is shared and tested once. |
| D5 | **Generation state (shuffle bag, marathon cursor) persists in `channel_schedule_state.cursor`** so top-ups continue rather than reshuffle. | Story EC1 — a top-up must continue the bag, not restart it. |
| D6 | **Padding is the packer's job, always to the boundary**, drawing filler (27.10) then falling back to up-next/tail-replay. | Story AC7 — contiguity is non-negotiable; no gaps ever. |
| D7 | **Smart-mix degrades to weighted shuffle** when classification is absent/disabled, logging the fallback. | Story AC6 — channels must work without Epic 26. |
| D8 | **Each block snapshots display metadata** (`title_snapshot` JSONB). | Story AC11 — cheap, stable guide reads decoupled from later library edits. |
| D9 | **Generation never raises; empty source → a single rolling slate block.** | Story AC10 — a degenerate library must still yield a defined timeline. |

---

## 1. Package layout (Pipeline Service, Python)

```
pipeline/src/maktaba_pipeline/channels/
├── __init__.py
├── pass_.py            # run_schedule(channel_id) + topup_all() — entry points (D1)
├── planner/
│   ├── __init__.py
│   ├── base.py         # Planner protocol; PlanItem dataclass (D4)
│   ├── shuffle.py      # fair-bag shuffle, no adjacent repeat (AC3)
│   ├── marathon.py     # series_episodes order + loop (AC4)
│   ├── schedule.py     # daypart slots + fill, tz-aware (AC5)
│   └── smartmix.py     # classification-driven daypart balance + fallback (AC6/D7)
├── packer.py           # lay PlanItems on the absolute timeline, pad to boundary (D2/D6)
├── slate.py            # empty-source rolling slate block (D9)
├── repo.py             # read channels/series/classification; write channel_programs (D3/D8)
└── tests/
    ├── test_packer_contiguity.py
    ├── test_shuffle.py
    ├── test_marathon.py
    ├── test_schedule_slots.py
    ├── test_smartmix.py
    ├── test_padding.py
    └── test_topup_immutable.py
```

## 2. The pass (`pass_.py`, D1/D3)

```python
async def run_schedule(conn, channel_id, *, now, horizon=timedelta(hours=48)):
    ch = await repo.load_channel(conn, channel_id)
    if not ch.enabled:
        return
    state = await repo.load_state(conn, channel_id)          # cursor/bag (D5)
    anchor = max(now, state.horizon_until or now)            # never rewrite past/current (D3)
    planner = make_planner(ch)                               # D4
    items = planner.plan(conn, ch, since=anchor, until=now + horizon, state=state)
    blocks = packer.pack(items, ch, start_at=anchor,
                         until=now + horizon, filler=load_filler(conn, ch))  # D6
    if not blocks:
        blocks = [slate.rolling(ch, anchor, now + horizon)]  # D9
    await repo.replace_future_blocks(conn, channel_id, from_at=anchor, blocks=blocks)  # D3
    await repo.save_state(conn, channel_id, planner.export_state())          # D5
```

`topup_all()` iterates enabled channels whose `horizon_until - now < 24h`
and extends them — the cron entry (every ~6 h). A debounced trigger fires
`run_schedule` when [27.1](plan-27-01-channel-definition.md) signals a
rule change.

## 3. The packer (`packer.py`, D2/D6) — the contiguity invariant

```python
def pack(items, ch, *, start_at, until, filler):
    blocks, t = [], start_at
    for item in items:
        if t >= until: break
        end = t + item.duration
        blocks.append(Block(kind="program", video_id=item.video_id,
                            start_at=t, end_at=end,
                            source_offset=item.offset,
                            source_duration=item.duration,
                            title_snapshot=item.snapshot))     # D8
        t = end
        t = pad_to_next_boundary(blocks, t, ch, filler, until) # D6 — fills any sub-slot gap
    # guarantee coverage to `until`
    if t < until:
        blocks += filler_or_slate(t, until, ch, filler)
    assert all(blocks[i].end_at == blocks[i+1].start_at        # D2 contiguity (tested)
               for i in range(len(blocks)-1))
    return blocks
```

Filler selection honours the channel policy (fit-preference,
max-consecutive) and **coalesces** a large gap filled by short clips into
a single looping filler block ([27.10](plan-27-10-filler-bumper-system.md),
Story EC8/AC4a).

## 4. Smart-mix (`smartmix.py`, D7)

Reads `video_classification` (content_type, length bucket) +
`video_topics`; assigns each daypart a target genre/length distribution
from `daypart_profile`; greedily fills the day picking items that move the
running distribution toward target (a simple proportional sampler, not a
model). If `video_classification` rows are missing for the library →
`weighted_shuffle_fallback()` and a logged warning.

## 5. Data model — migration slot 0082

`shared/db/migrations/0082_channel_programs.sql` (+ `.sqlite.sql`):

```sql
-- +goose Up
-- +goose StatementBegin
-- Slot 0082 (Epic 27 / Story 27.2) — the linear schedule (wall-clock anchored).
CREATE TABLE IF NOT EXISTS channel_programs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    seq             BIGINT      NOT NULL,                       -- monotonic per channel
    kind            TEXT        NOT NULL DEFAULT 'program'
                                CHECK (kind IN ('program','filler','bumper','slate')),
    video_id        UUID        REFERENCES videos(id) ON DELETE SET NULL,
    filler_item_id  UUID,                                       -- → filler_items (slot 0085)
    start_at        TIMESTAMPTZ NOT NULL,
    end_at          TIMESTAMPTZ NOT NULL,
    source_offset   INTEGER     NOT NULL DEFAULT 0,             -- ms into the source
    source_duration INTEGER     NOT NULL,                       -- ms played from the source
    title_snapshot  JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- D8: cached guide metadata
    CHECK (end_at > start_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Time-range lookups (guide + "what's on now" + live join) are the hot path.
CREATE INDEX IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);

CREATE TABLE IF NOT EXISTS channel_schedule_state (
    channel_id        UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    anchor_at         TIMESTAMPTZ,
    horizon_until     TIMESTAMPTZ,
    last_generated_at TIMESTAMPTZ,
    generator_version INTEGER     NOT NULL DEFAULT 1,
    cursor            JSONB       NOT NULL DEFAULT '{}'::jsonb,  -- D5: shuffle bag / marathon idx
    stale             BOOLEAN     NOT NULL DEFAULT false         -- set by rule change (27.1 D5)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_schedule_state;
DROP TABLE IF EXISTS channel_programs;
-- +goose StatementEnd
```

`.sqlite.sql` per convention. Register slot 0082 in `MANIFEST.md`.

## 6. Files to create / modify

**Create:** everything under `pipeline/.../channels/`, the migration pair.

**Modify:**
- `pipeline/.../orchestrator` or cron registry — register `topup_all` on
  a ~6 h schedule and the debounced `run_schedule` trigger.
- `shared/db/migrations/MANIFEST.md` — register slot 0082.
- The smart-query evaluator (Python side) — reuse for `source_filter`
  resolution; add any missing filter key (mirrors 26.4's note).

## 7. Dependencies

- **27.1** (`channels`), **Story 7.14** (smart-query for source
  resolution), **27.10** (`filler_items` for padding — soft dep; padding
  falls back to slate/up-next without it), **26.3** (`series_episodes`
  for marathon — only the marathon mode), **26.2/9.9**
  (`video_classification`/`video_topics` for smart-mix — soft dep, D7
  fallback).

## 8. Test strategy

`test_packer_contiguity` asserts the no-gap/no-overlap invariant on
random inputs; `test_topup_immutable` asserts past/current blocks are
byte-identical across regens; per-mode planner tests; `test_padding`
covers fit/coalesce/fallback; `test_smartmix` checks daypart distribution
within tolerance and the fallback path. A DST test covers EC3.

## 9. Performance

A 48 h schedule for one channel is ~hundreds of blocks; generation is
DB-read-bound, not CPU-bound (smart-mix's sampler is O(items)). Top-up
runs all channels in one pass, debounced — never per-request.
