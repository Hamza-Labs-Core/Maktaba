# Plan 33.2 — TV releases and snapshots — implementation

> Implementation plan for [story-33-02](story-33-02-releases-snapshots.md).
> Design: [design.md](design.md) §3. Migration: slot 0122 (`tv_releases`, `tv_release_channels`,
> `tv_routing`, `tv_sched_gen_seq`; `tv_changes`/`tv_change_events` land in the same file, used by 33.10).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Snapshot whole channel definitions, not diffs. | A slot needs the full picture; diffs are computed for display. |
| D2 | Content digest identifies a release. | No duplicates; a no-op change is detected. |
| D3 | Builder lives in the pipeline. | It reuses `write_channels`' plan-to-definition code (`_desired`, `look`, `_filler_spec`). |
| D4 | Settings snapshot = `rules.tv` + `rules.tv_output` only. | The sections that change what is on air. |
| D5 | Binaries side by side, checksum-checked by the supervisor. | Staging a version must not replace the live one. |

## 1. Files

**Create**
- `shared/db/migrations/0122_tv_releases_changes.sql` (+ sqlite)
- `pipeline/src/maktaba_pipeline/tvrelease/__init__.py`, `build.py` (`build(base, edits) → Release`), `snapshot.py` (from `channels`/`app_settings`), `digest.py`
- `streaming/internal/channel/repo_release.go` — Repo reading `tv_release_channels` + `sched_gen`
- `streaming/internal/channel/settings_release.go` — `Settings` from the release (tv-core `Settings` interface)
- `api/internal/handlers/tvchange/releases.go` — `GET /api/tv/releases`
- `cmd/maktaba-server/internal/supervisor/release.go` — resolve binary path + checksum
- tests

**Modify**
- `streaming/cmd_slot.go` — choose the release repo/settings when `MAKTABA_TV_RELEASE` is set
- `pipeline/src/maktaba_pipeline/engine/stages/write_channels.py` — factor `_desired`/look/filler into reusable functions (no behaviour change)
- channel art storage — content-addressed file names (if not already)

## 2. Steps

1. Migration + bootstrap function `tv_bootstrap_release()` called by the API at boot when no release.
2. Snapshot + digest (canonical JSON, `canonical_hash` from `engine/hashing.py`).
3. Release repo in Go, behind the existing `Repo` interface.
4. Supervisor resolves `bin/releases/<version>/maktaba-streaming[.exe]`; falls back to the current
   binary for release 1 when the versions match.
5. Releases API.

## 3. Tests

Python unit tests for digest and builder; Go tests for the release repo (Postgres job) and settings;
supervisor checksum test with a temp dir.

## 4. Rollout

Phase A. Release 1 is the live state; nothing else is built until 33.10.
