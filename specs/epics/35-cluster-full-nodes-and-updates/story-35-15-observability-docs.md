# Story 35.15 — Observability, docs and runbook

## Description

*As the owner, I want to see an update happening from the control centre and the metrics I already
use, and to have a page that tells me in plain words what happens at 03:00, how to stop it, and what
to do when something went wrong.*

Metrics, events and a control-centre view for the update flow and the full nodes, plus user and
operator documentation: a new updates page in the docs, the cluster docs updated for full nodes, and a
runbook for every failure of design §14.

Design: [design.md](design.md) §14, §16; README R11.

## Acceptance criteria

- **AC1** Metrics (Prometheus, the API's and the supervisor's `/metrics`), prefix `maktaba_update_`:
  `runs_total{result}`, `run_duration_seconds`, `step_duration_seconds{step}`,
  `migration_duration_seconds`, `backup_bytes`, `targets{state}`, `node_apply_duration_seconds`,
  `verify_failures_total{reason}`, `machines_behind`, `last_check_timestamp`. Full-node metrics:
  `maktaba_node_delegate_requests_total{result}`, `maktaba_node_delegate_cache_hits_total`,
  `maktaba_node_proxy_requests_total{class,status}`, `maktaba_node_paused{reason}`.
- **AC2** Events (structured logs and the hub's events file, Epic 31 §7.4): `update_planned`,
  `update_step_started|finished|failed`, `update_home_committed`, `update_rolled_back`,
  `update_node_prepared|applied|failed|held|caught_up`, `launcher_rollback`,
  `release_verify_failed`, `release_withdrawn`; each with `run_id`, `version`, `host_id`.
- **AC3** Control centre: the health samples (`health_samples`, Epic 22.9 / 33.15) gain
  `update.state` and `update.machines_behind`; the control centre shows a run card (version, step,
  targets done/total) linking to the Updates page.
- **AC4** Docs: a new `docs/updates.md` — what is automatic, the quiet window, Update now, skip,
  defer, pin, channels, what is verified (signature identity, checksums, hashes), what happens to TV,
  what nodes do, settings table (`rules.updates`, same names and defaults as the README); replaces the
  server part of `docs/auto-update.md` (which keeps desktop/mobile).
- **AC5** `docs/cluster/README.md` and `join-a-machine.md`: full nodes (open any machine, sign in,
  which pages proxy to the home), converting a worker-only node (`maktaba-server setup --node`,
  `install-node.sh --full`, `install-node.ps1 -Full`), Windows task vs service.
- **AC6** Runbook `docs/cluster/runbook-updates.md`, one entry per failure of design §14 plus:
  pausing all updates (`auto_install` off or Defer), rolling back the home or a node by hand
  (`maktaba-server update rollback`), restoring a backup (`maktaba-server restore --backup … --confirm`,
  when and what it loses), recovering a corrupt launcher, reading `updates/journal.json` and
  `updates/logs/<run_id>.log`, `maktaba-launcher --status`, verifying a release by hand with `cosign
  verify-blob` and the pinned identity, known issues from story 35.14.
- **AC7** `deploy/packaging/README.md` and `upgrade.md` describe the self-managed layout, the launcher
  and the package-managed / Homebrew / Docker cases; the release notes template in `release.yml` prints
  the exact pinned identity (not the loose regexp).
- **AC8** Every doc page exists in English; the in-app help links (Updates page, paused page) point to
  them. Arabic for in-app strings only (as the rest of the docs).

## Test cases

- **TC1** `TestUpdateMetricsRegistered` — every metric of AC1 present with its labels.
- **TC2** `TestUpdateEventsCarryRunID` — events emitted by a fake run carry `run_id`, `version`,
  `host_id`.
- **TC3** `TestHealthSamplesUpdateState`.
- **TC4** `ControlCentreUpdateCard.test.tsx`.
- **TC5** Docs link check (the repo's markdown link check, or a small script in CI) over `docs/` and
  this epic.
- **TC6** `TestReleaseNotesPinnedIdentity` — the generated notes contain the exact SAN for the tag.

## Edge cases

- **EC1** The supervisor's `/metrics` during its own restart: scrape gaps are expected; counters are
  reloaded from the journal and `update_runs` so they do not reset to zero mid-run in the UI.
- **EC2** A node without the full-node metrics (worker-only): absent series, not zeros.

## Dependencies

35.1–35.14. Epic 22.9 (health samples), Epic 33.15 (control centre), Epic 31 events file.
