# Implementation Plan — Story 24.7 Integrity verification

> Companion to [story-24-07-integrity-verification.md](story-24-07-integrity-verification.md).
> Story states *what* and *why*; this plan states *how*.
> The audit log table and writer is owned by
> [plan-21-06](../21-observability/plan-21-06-audit-log.md); this plan
> writes into it using the canonical column shape (category='data',
> event, target_id, target_kind, payload). **plan-21-06's migration
> must run before this plan's first insert**; CI's full-migration boot
> (plan-22-04) catches ordering errors.
> The `corrupted` state (lowercase) is pinned by plan-24-03's CHECK enum
> (Story 24.6 owns the FSM transition).
> **Cron daemon shared with plan-24-05** — this plan registers handlers
> against the same `robfig/cron/v3` instance; it does not run a second
> daemon.
> **`compute_content_hash` is owned by plan-24-08**; this plan imports
> from `domain.identity` and never reimplements.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Doctor entry | `maktaba-pipeline doctor --integrity`. Sub-flags: `--mode={sample,full}`, `--repair`, `--library NAME`. |
| Checks | `content_hash`, sidecar presence, DB referential, FTS/Chroma row-count parity. |
| Repair | Re-enqueue `processing_jobs` for missing sidecars; reindex via `reprocess --from-stage index` for parity drift. |
| Schedule | Weekly via the cron daemon in Story 24.5. Default off in single-user mode unless opted in. |
| Reports | Written to `audit_log` (category `data`); surfaced in admin panel. |
| Out of scope | DR scenarios (24.6); audit-log mechanics (21.6); cron daemon (24.5). |

## 1. Architecture diagram

```
   doctor --integrity --mode=sample --library X
                  │
                  ▼
   ┌───────────────────────────────┐
   │ checks                        │
   │  hash_recompute (sample)      │
   │  sidecar_presence             │
   │  fk_integrity                 │
   │  fts_chroma_parity            │
   └──────────────┬────────────────┘
                  │ findings
                  ▼
   audit_log (category=data, action=integrity.report)
                  │
                  ▼ admin UI
              report card
                  │ if --repair
                  ▼
   re-enqueue jobs / reprocess --from-stage index
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/cli/integrity.py` | CLI entry. |
| `pipeline/src/maktaba_pipeline/integrity/checks/hash_check.py` | Re-hash sample/full set. |
| `pipeline/src/maktaba_pipeline/integrity/checks/sidecar_check.py` | Walk `videos` rows; assert artifacts exist on disk. |
| `pipeline/src/maktaba_pipeline/integrity/checks/fk_check.py` | DB-side cross-table sanity. |
| `pipeline/src/maktaba_pipeline/integrity/checks/parity_check.py` | FTS / Chroma row-count comparison. |
| `pipeline/src/maktaba_pipeline/integrity/repair.py` | Re-enqueue + reindex helpers. |
| `pipeline/src/maktaba_pipeline/integrity/report.py` | Report aggregator + audit writer. |
| `web/src/routes/admin/integrity.tsx` | Admin panel. |
| Tests — `tests/integration/integrity_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/cli.py` | `doctor` subcommand grows `--integrity`. |
| `api/internal/http/admin_integrity.go` | `GET /api/admin/integrity/reports`. |
| `pipeline/src/maktaba_pipeline/tasks/scheduler.py` | Weekly cron entry. |

### 2.3 Doctor CLI

`cli/integrity.py`:

```python
@click.command()
@click.option("--mode", type=click.Choice(["sample", "full"]), default="sample")
@click.option("--repair", is_flag=True)
@click.option("--library", default=None, help="library name; default: all")
@click.option("--sample-rate", default=0.01,
              help="fraction sampled when mode=sample. Default 1%; for "
                   "a 50 k-video library this samples 500 files.")
async def integrity(mode, repair, library, sample_rate):
    async with db_conn() as db:
        report = Report()
        await hash_check.run(db, mode, sample_rate, library, report)
        await sidecar_check.run(db, library, report)
        await fk_check.run(db, report)
        await parity_check.run(db, report)
        await report.persist(db)            # writes audit_log row
        if repair:
            await repair_module.apply(db, report)
        click.echo(report.summary())
        sys.exit(0 if report.ok else 2)
```

### 2.4 Hash check

`hash_check.py`:

```python
from domain.identity import compute_content_hash  # owned by plan-24-08

async def run(db, mode, rate, library, report):
    # Lowercase states per architecture §7.2 / plan-24-03 CHECK enum.
    sql = "SELECT id, path, content_hash FROM videos WHERE state IN ('ready','ready_no_audio')"
    if library:
        sql += " AND library_id = (SELECT id FROM libraries WHERE name=$1)"
    rows = await db.fetch_all(sql, library) if library else await db.fetch_all(sql)
    if mode == "sample":
        rows = random.sample(rows, max(1, int(len(rows) * rate)))
    for r in rows:
        actual = compute_content_hash(r["path"])
        if actual != r["content_hash"]:
            report.add_finding(Finding(
                kind="hash_mismatch", video_id=r["id"], path=r["path"],
                expected=r["content_hash"], actual=actual))
            # Don't transition state from this check — that's the probe
            # stage's job (Story 24.6); we only report.
```

Sample mode keeps full-library scans bounded. Full mode requires `EC1`
opt-in plus a confirmation prompt.

### 2.5 Sidecar presence

`sidecar_check.py`:

```python
async def run(db, library, report):
    # For each ready video, expected artifacts: vtt, segments.json,
    # poster, sprites grid.
    rows = await db.fetch_all(
        "SELECT id, path FROM videos WHERE state='ready'"
        + (" AND library_id = (SELECT id FROM libraries WHERE name=$1)" if library else ""),
        *(((library,) if library else ()))
    )
    for r in rows:
        sc = sidecar_dir(r["path"])
        for art in ("transcript.vtt", "segments.json", "poster.jpg", "sprites/sheet.jpg"):
            p = sc / art
            if not p.exists():
                report.add_finding(Finding(
                    kind="sidecar_missing", video_id=r["id"], path=str(p)))
```

### 2.6 FK / referential check

`fk_check.py`:

```python
QUERIES = [
    ("dangling_transcript_segments",
     "SELECT ts.id FROM transcript_segments ts "
     "LEFT JOIN transcripts t ON t.id=ts.transcript_id "
     "WHERE t.id IS NULL"),
    ("dangling_jobs",
     "SELECT j.id FROM processing_jobs j LEFT JOIN videos v ON v.id=j.video_id WHERE v.id IS NULL"),
    ("orphan_acl",
     "SELECT a.user_id, a.library_id FROM library_acl a "
     "LEFT JOIN libraries l ON l.id=a.library_id WHERE l.id IS NULL"),
]

async def run(db, report):
    for kind, sql in QUERIES:
        rows = await db.fetch_all(sql)
        for r in rows:
            report.add_finding(Finding(kind=kind, detail=dict(r)))
```

These should be empty — FK CASCADE prevents the conditions — but the
check is defense-in-depth. `--repair` does not auto-delete dangling
rows (too risky); the report tells ops to investigate.

### 2.7 Parity check

`parity_check.py`:

```python
async def run(db, report):
    # Canonical table names per architecture §8.1:
    #   transcript_segments (not "segments")
    #   transcripts_fts     (not "segments_fts")
    s = await db.fetch_one("SELECT count(*) AS c FROM transcript_segments")
    fts = await db.fetch_one("SELECT count(*) AS c FROM transcripts_fts")
    if abs(s["c"] - fts["c"]) > 0:
        report.add_finding(Finding(
            kind="fts_drift",
            detail={"transcript_segments": s["c"], "transcripts_fts": fts["c"]}))
    # Chroma count via the search adapter.
    chroma = await search.chroma_count()
    if abs(s["c"] - chroma) > 0:
        # Allow a small tolerance for in-flight indexing; cross-reference
        # processing_jobs state to filter (EC2). Lowercase 'running' per
        # plan-24-03 CHECK.
        in_flight = await db.fetch_one(
            "SELECT count(*) AS c FROM processing_jobs WHERE state='running' AND stage='index'")
        if abs(s["c"] - chroma) > in_flight["c"]:
            report.add_finding(Finding(
                kind="chroma_drift",
                detail={"transcript_segments": s["c"], "chroma": chroma}))
```

### 2.8 Repair

`repair.py`:

```python
async def apply(db, report):
    for f in report.findings:
        if f.kind == "sidecar_missing":
            await db.execute(
                "INSERT INTO processing_jobs(video_id, stage, state) "
                "VALUES ($1, $2, 'pending') ON CONFLICT DO NOTHING",
                f.video_id, _stage_for_artifact(f.path))
        elif f.kind in {"fts_drift", "chroma_drift"}:
            await reprocess_from_stage(db, "index")
        elif f.kind == "hash_mismatch":
            # Don't auto-repair; the probe stage handles state transitions.
            pass
        elif f.kind in {"dangling_transcript_segments", "dangling_jobs", "orphan_acl"}:
            # Auto-deleting these is risky — leave as report only.
            pass
```

### 2.9 Reporting

`report.py`:

```python
@dataclass
class Finding:
    kind: str
    video_id: str | None = None
    path: str | None = None
    expected: str | None = None
    actual: str | None = None
    detail: dict | None = None

class Report:
    def __init__(self): self.findings: list[Finding] = []
    @property
    def ok(self): return not self.findings
    def add_finding(self, f): self.findings.append(f)

    async def persist(self, db):
        # Canonical audit_log shape per plan-21-06 / PLAN_REVIEW §1.4:
        #   id, category, event, actor_user, actor_ip, target_id,
        #   target_kind, payload, dedupe_key, created_at.
        # category='data' for integrity-verification rows.
        await db.execute(
            "INSERT INTO audit_log(id, category, event, target_kind, "
            "                       payload, created_at) "
            "VALUES (gen_random_uuid(), 'data', 'integrity.report', "
            "        'library', $1::jsonb, now())",
            json.dumps([asdict(f) for f in self.findings]))

    def summary(self) -> str:
        if self.ok: return "Integrity check passed (no findings)."
        from collections import Counter
        kinds = Counter(f.kind for f in self.findings)
        return "\n".join(f"  {k}: {v}" for k, v in kinds.items())
```

The admin UI's `/api/admin/integrity/reports` endpoint reads recent
audit_log rows of action `integrity.report` and renders them as cards.

## 3. Test plan

### 3.1 Detection (TC1)

| Test | What it pins |
|---|---|
| `TestSidecarMissingDetected` | Delete `transcript.vtt`; `doctor --integrity` reports `sidecar_missing`. |
| `TestSidecarRepairReenqueues` | Same scenario with `--repair`; a `processing_job(stage=subtitle_gen, state='pending')` is added (lowercase per plan-24-03 CHECK). |
| `TestHashMismatchDetected` | Flip a byte; sample mode happens to include the file (forced via fixture); finding `hash_mismatch` written to audit log via the canonical (category='data', event='integrity.report') shape. |

### 3.2 FTS parity (TC2)

| Test | What it pins |
|---|---|
| `TestFtsParityDriftDetected` | Direct DELETE on `transcripts_fts` for one row; doctor reports `fts_drift`. |
| `TestChromaParityDriftDetected` | Drop 100 docs from chroma; doctor reports `chroma_drift`. |
| `TestRepairFtsReindexes` | `--repair` enqueues `index` stage; subsequent doctor returns ok. |

### 3.3 Sample mode (TC3)

| Test | What it pins |
|---|---|
| `TestSampleModeBoundedTime` | 50 k synthetic video rows; doctor sample mode completes ≤ 5 min on a representative runner. |
| `TestFullModeRequiresOptIn` | `--mode=full` without `--accept-full` exits with a confirmation prompt; non-interactive runs refuse. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| 30 TB hash recompute (EC1) | Sample mode default; full mode requires `--accept-full`; the warning prints estimated wall-clock. | `TestFullModeRequiresOptIn` |
| Drift caused by in-flight job (EC2) | Parity check subtracts `running` index jobs from the difference; only the residual is reported. | `TestParityIgnoresInflight` |
| Clock skew / mtime regression (EC3) | Doctor uses `content_hash`, never mtime, as truth. The hash check reads bytes, not metadata. | `TestMtimeSkewDoesNotConfuse` |
| Dangling FK rows in `--repair` | Not auto-deleted; the report tells ops to inspect. The audit log carries the IDs. | `TestDanglingNotAutoDeleted` |
| Library deletion during the check | The hash/sidecar SQL fetches `(id, path, content_hash)` once at the start of the run. If the library's videos rows are deleted mid-run via FK CASCADE, the loop's per-row reads continue against the in-memory snapshot; videos whose files are gone surface as `read_error` findings (NOT as `sidecar_missing`, since the SQL query already returned the rows). The check completes; the report includes a `library_deleted_mid_run=true` annotation. | `TestLibraryDeletedMidRun` |
| Massive findings list | The audit-log payload column is JSONB; if findings exceed 1 MB, the report is split into multiple audit rows tagged with the same `run_id`. | `TestLargeReportSplit` |
| Default off in single-user mode | The cron entry checks `auth.multi_user || tasks.integrity_force_on`; defaults off when `auth.multi_user=false`. | `TestSingleUserModeDefaultOff` |
| Repair triggers a reprocess loop | The `index` stage's idempotency key (Story 24.2) prevents duplicate work; a second consecutive doctor run sees no findings. | `TestRepairConverges` |
| Hash check on a file with active writes | The path canonicalizer (Story 23.5) and the scanner already gate on mtime stability; hash check reads in best-effort mode and reports `read_error` if the file is mid-write. | `TestActiveWriteSurfacesReadError` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `click` | already | CLI. |
| `random.sample` | stdlib | Sample selection. |
| `dataclasses` | stdlib | Finding/Report. |

## 6. Acceptance checklist

**Checks**
- [ ] `content_hash` re-verification with sample/full modes.
- [ ] Sidecar presence walks all `ready` (lowercase) videos.
- [ ] FK referential integrity queries.
- [ ] FTS / Chroma parity vs `transcript_segments` row count (canonical names).

**Repair**
- [ ] `--repair` re-enqueues missing sidecars.
- [ ] Index drift triggers `reprocess --from-stage index`.
- [ ] Dangling rows are reported only.

**Schedule**
- [ ] Weekly cron entry; configurable cadence.
- [ ] Default off in single-user mode unless opted in.

**Surface**
- [ ] Findings written to `audit_log` (category `data`).
- [ ] Admin UI lists recent reports.
