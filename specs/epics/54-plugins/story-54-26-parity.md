# Story 54.26 — The parity proof

> Epic 54 · Phase 4 · Depends on: 54.25 · Needed by: every wave (54.28–54.36)

## Why

A stage that writes even slightly different rows, files or fingerprints after it moves would
rerun on every subject, or quietly change the library, and the drift would compound down the
graph (H20). Each wave must prove, in CI, that its stages do **exactly** what the in-process
code did, and are not much slower.

## Description

### 1. The fixture

`shared/fixtures/parity/`:

- `seed.sql`: a Postgres dump of a small, rich home: 4 libraries (series, clips, a YouTube
  library, a converted library), about 200 videos with Arabic and English titles, transcripts
  with segments and words, tags, topics, chapters, duplicates and a file trash, TV channels
  with a plan, Plex and dizqueTV extension rows, `rules.*` with every section set.
- the sample media in `shared/fixtures/samples/` mapped as the libraries' files.
- `recordings/`: deterministic fakes with recorded answers: the STT backend (segments and words
  per sample clip), the language model (answers keyed by prompt hash), YouTube listings and
  downloads (`yt-dlp` fake binary on `PATH`), Plex and dizqueTV (HTTP fixtures behind the
  service door), the encyclopedia.

The fixture is built by `tools/plugins/parity_seed.py` from the existing test factories, so it
follows the schema; CI regenerates it when migrations change.

### 2. The run

`tools/plugins/parity.py --stages <list>`:

1. restore `seed.sql` into two databases, A and B;
2. queue the same jobs for each stage (every subject it applies to in the fixture, plus the
   plugin's own `parity.toml` cases);
3. run them with `impl = inproc` on A and `impl = plugin` on B (real runner, real API,
   statement registry, roles, sandbox), in dependency order, with the fakes;
4. compare A and B.

### 3. What must be identical

| What | How compared |
|---|---|
| Rows of every table in the plugin's `db.write` | canonical JSON per row, sorted by primary key; only the columns in `parity.toml` `ignore` (such as `updated_at`) are masked; ids are deterministic in the fixture |
| Files written | sha256 of every file under the areas in `files.write`, with paths relative to the area |
| Each job's `StageResult` | `output_hash`, `outcome`, `requests`, `changed_subjects`, `summary`, `input_hash` |
| Fingerprints | input and output fingerprints of every subject for the stage, before and after the run |
| Queue effects | jobs enqueued (stage, subject, reason, priority, payload), `job_events` rows |
| Failures | for the plugin's `parity.toml` failure cases (a missing file, a corrupt file, a fake that errors): the same failure cause and class |

Any difference fails the job and prints the first 20 differing rows or files.

### 4. A real model, once

One run of the smallest CPU Whisper model (`faster-whisper` `tiny`, fixed seed, one thread) on
three sample clips through both paths, compared byte for byte; it catches a difference in how
audio reaches the model, which the fake can't.

### 5. Performance

Per stage, on the same runner, the p50 job time must be ≤ 1.10× and the p95 ≤ 1.15× of the
in-process path (N16), and the plugin API calls per job are reported. The job keeps the
previous run's numbers as an artifact and fails if calls per job grow more than 10 % without
a `parity.toml` note.

### 6. Where it runs

`plugins-parity` on every PR touching `plugins/first-party/`, `plugins/sdk/python/`,
`pipeline/src/maktaba_plugin_runner/`, `api/internal/plugins/statements/` or
`pipeline/src/maktaba_pipeline/engine/` (only the stages whose plugins changed, or all when the
runner, registry or engine changed), and nightly for all migrated stages. It keeps the
runner's and API's logs in its report (standing rule).

## Acceptance criteria

- **Given** a migrated stage whose plugin writes one row with a different value,
  **when** `plugins-parity` runs,
  **then** it fails naming the table, the key and the column.
- **Given** a plugin that computes an input fingerprint differently for one subject,
  **when** it runs,
  **then** it fails naming the subject.
- **Given** a plugin that is 20 % slower at p50 on its stage,
  **when** it runs,
  **then** it fails with both numbers.
- **Given** an identical plugin,
  **when** it runs,
  **then** it passes and attaches its numbers and logs.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | self-test | a toy stage with a planted row difference | parity | fails naming it |
| T02 | self-test | planted file difference | parity | fails naming the file |
| T03 | self-test | planted fingerprint difference | parity | fails naming the subject |
| T04 | self-test | planted slowdown (sleep) | parity | fails N16 |
| T05 | self-test | identical toy stage | parity | passes |
| T06 | unit (py) | `parity.toml` schema | validate | unknown keys refused |
| T07 | CI | nightly | all migrated stages | green, logs attached |

## Edge cases

- **Time-dependent output** (dates in summaries): the fixture freezes the clock for both runs
  (`MAKTABA_FAKE_NOW`), so nothing is masked that does not have to be.
- **Order-dependent output** (two jobs racing on one library): parity runs jobs serially in
  dependency order on both sides.
- **A stage that changes on purpose in the same PR**: not allowed; behaviour changes go in their
  own PR before or after the move (the golden graph and parity both stay meaningful).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `tools/plugins/parity.py`, `parity_seed.py` (new) | §2–§5. |
| `shared/fixtures/parity/` (new) | §1. |
| `plugins/sdk/python/maktaba_plugin_sdk/parity.py` (new) | `parity.toml` schema and helpers. |
| `.github/workflows/` `plugins-parity` job | §6, with log artifacts. |

### Tests

- Self-tests T01–T05, unit T06, CI T07.

### Rollout

The job is added green with no migrated stage (it runs its self-tests only).

### Rollback

Not applicable (CI only).

### Dependencies

54.25 (the switch, so both paths run in one build).
