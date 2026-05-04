# Implementation Plan — Story 24.6 Disaster recovery

> Companion to [story-24-06-disaster-recovery.md](story-24-06-disaster-recovery.md).
> Story states *what* and *why*; this plan states *how*.
> Backup/restore primitives from
> [Story 24.5](plan-24-05-backup-restore.md). State machine from
> architecture §3 (including the `CORRUPTED` state).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| DR doc | `docs/operations/disaster-recovery.md`. Each scenario is a section with steps, RTO/RPO, and a copy-paste command block. |
| `dr-drill` make target | `make dr-drill` runs scenario #1 against a seeded fixture; CI runs it nightly. |
| Admin Restore UI | A `web/src/routes/admin/recovery.tsx` page renders one card per scenario with a "Run" button that calls `/api/admin/recovery/<scenario>`. |
| Corrupted detection | Story 24.7 owns the `content_hash` re-verification; this story wires the FSM transition to `CORRUPTED` on mismatch. |
| Out of scope | Scenario-#3 detection mechanics (Story 24.7); the binary corruption recovery (Story 22.7); media-volume backup (out of scope by AC). |

## 1. Architecture diagram

```
   admin clicks Run on scenario card
            │
            ▼
   POST /api/admin/recovery/<id>
            │
            ▼
   ┌────────────────────────────┐
   │ recovery.Run(scenario)     │
   │  1. db_lost: pg_restore     │
   │  2. db_and_caches_lost:     │
   │       restore + reprocess   │
   │  3. media_corrupt: doctor   │
   │       + state=CORRUPTED     │
   │  4. binaries_corrupt:       │
   │       point at install path │
   └──────────┬─────────────────┘
              │ progress events
              ▼
       /api/ws/recovery/<id>  → UI live updates
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `docs/operations/disaster-recovery.md` | Operator-facing playbook. |
| `api/internal/recovery/scenarios.go` | The four scenarios as code. |
| `api/internal/http/admin_recovery.go` | HTTP + WebSocket handlers. |
| `api/cmd/api/dr_drill.go` | `make dr-drill` entry point. |
| `web/src/routes/admin/recovery.tsx` | UI (one card per scenario). |
| `web/src/components/RecoveryCard.tsx` | Reusable card. |
| Tests — `tests/integration/dr_drill_*.py`, `tests/e2e/dr_ui.spec.ts`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/stages/probe.py` | On `content_hash` mismatch, transition video to `CORRUPTED` and write an audit row. |
| `Makefile` | `dr-drill` target. |
| `.github/workflows/_nightly.yml` | Runs `dr-drill` once daily; alerts on failure. |

### 2.3 Disaster recovery doc (excerpt)

```
# Disaster Recovery

Goal: restore service after each of the failure modes below within
the documented RTO/RPO.

## Scenario 1 — DB lost, media intact

RTO: ≤ 30 minutes. RPO: ≤ 24 h (last daily backup).

1. Stop the API and Pipeline services.
2. Identify the most recent backup:
     maktaba-api backup list
3. Restore:
     maktaba-api restore --from <file> --confirm RESTORE
4. Run pending migrations:
     maktaba-api migrate up
5. Restart services:
     docker compose up -d
6. Reprocess any media added since the backup:
     maktaba-pipeline scan --library all
7. Verify:
     maktaba-pipeline doctor

## Scenario 2 — DB and caches lost, media intact

RTO: proportional to library size. RPO: same as Scenario 1.

Same as Scenario 1, then:

  maktaba-pipeline reprocess --from-stage index --library all

## Scenario 3 — Media partially corrupted

The next integrity sweep (Story 24.7) detects content_hash mismatches.
Affected videos transition to state=CORRUPTED. Operators triage from
the admin Recovery page.

## Scenario 4 — Service binaries corrupted

Reinstall via the canonical path:

  brew reinstall maktaba/tap/maktaba   (Mac)
  apt install --reinstall maktaba       (Linux)

State (DB + media) is intact.
```

### 2.4 Scenario implementations

`scenarios.go`:

```go
type Scenario string

const (
    SDbLost           Scenario = "db_lost"
    SDbAndCaches      Scenario = "db_and_caches_lost"
    SMediaCorrupt     Scenario = "media_corrupt"
    SBinariesCorrupt  Scenario = "binaries_corrupt"
)

type Step struct {
    Name string
    Run  func(ctx context.Context, ev chan<- Event) error
}

func StepsFor(s Scenario, deps Deps) []Step {
    switch s {
    case SDbLost:
        return []Step{
            {Name: "stop-services",       Run: deps.StopAll},
            {Name: "find-latest-backup",  Run: deps.FindLatestBackup},
            {Name: "pg-restore",          Run: deps.PgRestore},
            {Name: "migrate-up",          Run: deps.MigrateUp},
            {Name: "start-services",      Run: deps.StartAll},
            {Name: "scan-libraries",      Run: deps.ScanAll},
            {Name: "doctor",              Run: deps.Doctor},
        }
    case SDbAndCaches:
        return append(StepsFor(SDbLost, deps),
            Step{Name: "reprocess-from-index", Run: deps.ReprocessFromIndex})
    case SMediaCorrupt:
        return []Step{
            {Name: "doctor-integrity",   Run: deps.DoctorIntegrity},
            {Name: "list-corrupted",     Run: deps.ListCorruptedVideos},
        }
    case SBinariesCorrupt:
        return []Step{
            {Name: "point-at-installer", Run: deps.PrintInstallInstructions},
        }
    }
    return nil
}
```

Each `Step.Run` emits structured `Event`s the WebSocket handler
forwards to the UI: `{"step": "pg-restore", "phase": "running", "msg":
"Restoring 4 GiB"}`.

### 2.5 HTTP + WS handler

`admin_recovery.go`:

```go
// POST /api/admin/recovery/{scenario}  — admin only, audit category 'admin'.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
    _ = authz.Authorize(r.Context(), authz.AdminLibrary, authz.SystemResource{})
    s := Scenario(chi.URLParam(r, "scenario"))
    runID := uuid.NewString()
    ch := make(chan Event, 64)
    h.runs.Store(runID, ch)
    go func() {
        defer h.runs.Delete(runID)
        defer close(ch)
        for _, step := range StepsFor(s, h.deps) {
            ch <- Event{Step: step.Name, Phase: "running"}
            if err := step.Run(r.Context(), ch); err != nil {
                ch <- Event{Step: step.Name, Phase: "failed", Msg: err.Error()}
                return
            }
            ch <- Event{Step: step.Name, Phase: "done"}
        }
    }()
    json.NewEncoder(w).Encode(map[string]string{"run_id": runID})
}

// GET /api/admin/recovery/{run_id}/events  — WebSocket forward.
```

### 2.6 Make target + nightly job

`Makefile`:

```make
dr-drill:    ## Run scenario-1 DR drill against a seeded fixture
	@docker compose -f deploy/compose/docker-compose.yml \
	                -f tests/dr/docker-compose.fixture.yml up -d --wait
	@tests/dr/drill.sh   # asserts RTO budget and smoke
	@docker compose -f deploy/compose/docker-compose.yml down
```

`_nightly.yml`:

```yaml
on:
  schedule: [{ cron: "0 2 * * *" }]
jobs:
  dr-drill:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - run: make dr-drill
      - if: failure()
        uses: ./actions/alert
        with: { channel: ops, message: "DR drill failed" }
```

`tests/dr/drill.sh` is the golden-path script:

```bash
#!/usr/bin/env bash
set -euo pipefail
start=$(date +%s)
maktaba-api backup run
PGPASSWORD=$PG_PASS dropdb --if-exists maktaba && createdb maktaba
maktaba-api restore --from "$(maktaba-api backup list --json | jq -r '.[0].file')" --confirm RESTORE
maktaba-api migrate up
maktaba-pipeline scan --library all
end=$(date +%s)
elapsed=$((end - start))
if (( elapsed > 1800 )); then
  echo "RTO exceeded: ${elapsed}s > 1800s"; exit 1
fi
tests/dr/smoke.sh
```

### 2.7 Corrupted-state transition

`pipeline/.../probe.py`:

```python
async def probe_video(video, db) -> Probe:
    actual = compute_content_hash(video.path)
    if video.content_hash and actual != video.content_hash:
        await db.execute(
            "UPDATE videos SET state='CORRUPTED' WHERE id=$1", video.id)
        await audit.append(category="data", action="video.corrupted",
                           resource={"type": "video", "id": str(video.id)},
                           detail={"expected_hash": video.content_hash, "actual": actual})
        raise CorruptedMedia(video.path)
    return run_ffprobe(video.path)
```

The state CHECK constraint (Story 24.3) lists `CORRUPTED` so the
update succeeds.

## 3. Test plan

### 3.1 Scenario 1 (TC1)

| Test | What it pins |
|---|---|
| `TestDrDrillScenario1WithinRTO` | `make dr-drill` runs end-to-end within 30 minutes on the CI fixture; smoke passes. |
| `TestDrDrillEventsEmittedOrder` | The WebSocket emits `running → done` for each step; order matches `StepsFor`. |
| `TestDrDrillFailsLoud` | Inject a failure in `pg-restore`; the run emits `failed` with the error; the run halts at that step. |

### 3.2 Scenario 3 — corrupted media (TC2)

| Test | What it pins |
|---|---|
| `TestCorruptByteFlip` | Modify a byte in a fixture video; next probe transitions video to `CORRUPTED`; the audit row records expected vs actual hash. |
| `TestCorruptedShownInUI` | Admin recovery card lists corrupted videos via `GET /api/videos?state=CORRUPTED`. |

### 3.3 Documented commands (TC3)

| Test | What it pins |
|---|---|
| `TestEveryDocCommandIsExercised` | A test that parses `disaster-recovery.md` for fenced bash blocks and asserts each command appears in the drill script (or has an explicit `# manual` annotation). |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Partial DB restore (EC1) | `restore.go` refuses partial restores by default; `--allow-partial` flag with documented risk. | `TestPartialRestoreRefused` |
| Higher schema version (EC2) | `migrate up` runs forward automatically after restore; the doctor reports the delta. | `TestRestoreThenMigrateUp` |
| Lower schema version (EC3) | Refused by the migrate doctor at boot; clear error tells operator to upgrade first. | `TestRestoreLowerVersionRefused` |
| Drill timing variability | `RTO_BUDGET_SECONDS` env override lets CI runners with slow disks raise the threshold without changing code. | `TestRtoBudgetEnvOverride` |
| Concurrent admin runs | `runs` map is keyed by `run_id`; a second admin starting the same scenario gets a different run; both produce audit rows. The implementation does not serialize, but documents that concurrent destructive recovery is the operator's responsibility. | `TestConcurrentRecoveryRunsTwoAudits` |
| Media checksum mismatch on a file actively being written | The probe stage's pre-condition is "file mtime stable for ≥ 60 s" (Epic 1 scanner); writers in flight don't get probed. The corrupted-state transition only fires on stable files. | `TestActiveWriteSkipsCorruptionCheck` |
| `dr-drill` fixture polluted by a previous run | The drill compose stack uses ephemeral named volumes; `down -v` happens between runs. | `TestDrDrillFreshFixture` |
| WebSocket disconnect mid-recovery | The run continues server-side; the client polls `GET /api/admin/recovery/<run_id>` for state on reconnect. | `TestRecoveryResumeAfterDisconnect` |
| User triggers Restore UI without prior backup | The first step emits `failed` with `no-backups-found` and points at the backup CLI. | `TestNoBackupSurfacedAsError` |
| Drill exceeds budget repeatedly | Nightly alert fires; ops reviews and either bumps RTO or fixes regression. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `gorilla/websocket` | already | Live progress to UI. |
| `pg_dump`/`pg_restore` | matches PG | Restore. |

## 6. Acceptance checklist

**Doc**
- [ ] `docs/operations/disaster-recovery.md` exists with four scenarios.
- [ ] Every step has a copy-paste command.

**Drill**
- [ ] `make dr-drill` runs scenario 1.
- [ ] Nightly CI workflow runs the drill and alerts on failure.
- [ ] RTO budget enforced.

**UI**
- [ ] Admin Recovery page renders one card per scenario.
- [ ] WebSocket streams per-step progress.

**State**
- [ ] Probe transitions to `CORRUPTED` on hash mismatch.
- [ ] Audit row written.
