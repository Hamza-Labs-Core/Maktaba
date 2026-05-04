# Implementation Plan — Story 22.6 Upgrade and rollback

> Companion to [story-22-06-upgrade-rollback.md](story-22-06-upgrade-rollback.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the migration discipline from
> [Story 22.4](plan-22-04-database-migrations.md), the release flow from
> [Story 22.5](plan-22-05-release-management.md), and the forward-/back-
> compat invariants from [Story 24.9](../24-data-integrity/plan-24-09-forward-back-compat.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Canonical upgrade path | `git pull && docker compose pull && docker compose up -d`, documented in `UPGRADING.md`. |
| Rollback path | `git checkout v<previous>` then `docker compose pull && up -d`. Forward-compat invariant from Story 24.9 makes this safe across one minor. |
| Doctor binary | `maktaba-api migrate doctor` (extends the doctor from Story 22.4 with rollback simulation + duration estimate). |
| Long-migration ack | `--accept-long-migration` flag (defined in Story 22.4); referenced here to bind the operator UX. |
| Rolling-restart support | Each Go service exposes `/admin/drain` and `/healthz` on the admin mux (Story 21.4 hosts the mux). `/admin/drain` flips readyz to NOT_READY, stops accepting new sessions, and waits for inflight to drain. The upgrade script calls these via HTTP — there are no `/usr/local/bin/drain` or `/usr/local/bin/healthcheck` binaries to ship. The streaming service's session reaper allows in-flight HLS requests to complete (architecture §4 / Epic 8). |
| Backup before upgrade | `tools/upgrade.sh --backup-before-upgrade` triggers Epic 24.5's `backup-restore` flow before touching images; default off so the script remains fast for ops who already snapshot at the volume layer. |
| Config-migrate subcommand owner | `maktaba-api config migrate` is owned by **this plan**. Implemented in `api/cmd/api/config_migrate.go`; called by `tools/upgrade.sh` between drain and start when `MAKTABA_CONFIG_SCHEMA_VERSION` advances. |
| Out of scope | DB schema design (Epics 1–10 own that); CI release flow (22.5); on-disk artifact compatibility (24.9). |

## 1. Architecture diagram

```
┌──────────────────┐
│ pre-upgrade      │
│  doctor          │ ─── pg_dump → tmp DB → simulate migrations → estimate
└────────┬─────────┘
         │
         ▼
┌──────────────────┐                ┌────────────────┐
│ docker compose   │  rolling       │ /admin/drain   │
│ pull && up -d    │  per service ─►│ wait until idle│
└────────┬─────────┘                └────────────────┘
         │
         ▼
┌──────────────────┐
│ post-upgrade     │
│ smoke test       │ ─── curl /api/health, GET /api/system/version
└──────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `UPGRADING.md` | One-page operator guide. |
| `tools/upgrade.sh` | Wraps the pull/up/health-wait loop; idempotent. Supports `--backup-before-upgrade` to invoke Epic 24.5's `maktaba-backup snapshot` first. |
| `tools/rollback.sh` | Wraps the rollback flow. |
| `tools/rollback-simulator.go` | CI-time test harness: applies the new release's migrations to a fresh DB, then boots the *previous* release's binary against the migrated schema. Asserts no crash + no panic on the read paths declared by Story 24.9's forward-compat invariant. Catches schema-shape changes that the older binary cannot tolerate. |
| `api/cmd/api/migrate_doctor.go` | Extends Story 22.4's doctor with rollback simulation + UX. |
| `api/cmd/api/config_migrate.go` | `maktaba-api config migrate` subcommand. Reads `MAKTABA_CONFIG`, applies in-place schema upgrades when `MAKTABA_CONFIG_SCHEMA_VERSION` advances, writes atomically (`.tmp` then rename). Owned by this plan. |
| `api/internal/http/admin_drain.go` | `/admin/drain` handler — registered on the admin mux (Story 21.4). Flips readyz to NOT_READY, stops accepting new sessions, long-polls until in-flight requests complete or the deadline lapses. |
| `streaming/internal/http/admin_drain.go` | Same surface for streaming. |
| `pipeline/src/maktaba_pipeline/admin/drain.py` | Pipeline drain via gRPC `Diagnostics.Drain` (refuses new claims; finishes current). |
| `tools/version-jump-guard.sh` | Refuses two-minor jumps; correctly handles pre-release tags (`1.2.3-rc.4` is treated as `1.2.3` for ordering, not as a major-jump trigger); documents the supported v1.0 → v1.1 → v1.2 path. |
| `tests/upgrade/forward_back_test.sh` | Exercises TC1. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/cmd/api/main.go` | `serve` registers `/admin/drain`; SIGTERM triggers same flow. |
| `streaming/cmd/streaming/main.go` | Same. |
| `pipeline/src/maktaba_pipeline/cli.py` | `maktaba-pipeline drain` CLI subcommand. |
| `deploy/compose/docker-compose.yml` | `stop_grace_period: 60s` for api and streaming; pipeline gets 5min for in-flight transcribes. |

### 2.3 Upgrade script

`tools/upgrade.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT=${MAKTABA_ROOT:-/opt/maktaba}
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
BACKUP=0
for arg in "$@"; do
  case "$arg" in
    --backup-before-upgrade) BACKUP=1 ;;
  esac
done

cd "$ROOT"

if (( BACKUP )); then
  echo "==> Snapshot via maktaba-backup (Epic 24.5)"
  $COMPOSE exec -T api maktaba-backup snapshot --reason pre-upgrade
fi

echo "==> Pre-upgrade doctor"
$COMPOSE exec -T api maktaba-api migrate doctor --emit-json > /tmp/doctor.json
# `bc -l` returns the empty string when the input is empty or non-
# numeric, which expands to a syntax error inside (( … )). Default to
# zero so the comparison is well-defined even if the doctor JSON is
# missing the field.
duration=$(jq -r '.longest_statement_seconds // 0' /tmp/doctor.json)
duration=${duration:-0}
if (( $(echo "${duration:-0} > 30" | bc -l) )); then
  echo "Longest migration estimated at ${duration}s. Re-run with:"
  echo "  ACCEPT_LONG_MIGRATION=1 tools/upgrade.sh"
  if [[ -z "${ACCEPT_LONG_MIGRATION:-}" ]]; then exit 2; fi
fi

echo "==> Pulling new images"
$COMPOSE pull

# Each service exposes `/admin/drain` and `/healthz` on the admin mux
# at port 9100 (Story 21.4). The script hits these via HTTP from inside
# the container — no `/usr/local/bin/{drain,healthcheck}` binaries are
# shipped (PLAN_REVIEW §1.11).
admin_curl() {
  local svc="$1" path="$2"
  $COMPOSE exec -T "$svc" curl -fsS "http://127.0.0.1:9100${path}"
}

echo "==> Config schema migration (if any)"
$COMPOSE exec -T api maktaba-api config migrate --quiet || true

echo "==> Rolling restart"
for svc in api streaming pipeline web caddy; do
  echo "  -> $svc"
  if [[ "$svc" == "api" || "$svc" == "streaming" || "$svc" == "pipeline" ]]; then
    # Best-effort drain — a service that hasn't yet adopted the
    # admin-mux endpoint just gets the default container stop, which
    # is still graceful via `stop_grace_period`.
    admin_curl "$svc" "/admin/drain?timeout=120" || true
  fi
  $COMPOSE up -d --no-deps "$svc"
  if [[ "$svc" == "api" || "$svc" == "streaming" || "$svc" == "pipeline" ]]; then
    admin_curl "$svc" "/healthz" || \
      { echo "service $svc unhealthy after upgrade"; exit 3; }
  fi
done

echo "==> Post-upgrade smoke"
curl -fsS http://localhost:8080/api/health > /dev/null
old_ver=$(jq -r '.version // "unknown"' /tmp/doctor.json)
new_ver=$(curl -s http://localhost:8080/api/system/version | jq -r .tag)
echo "Upgraded $old_ver -> $new_ver"
```

The drain step is best-effort (`|| true`): a service that doesn't yet
expose `/admin/drain` (e.g., during a partial rollout) just gets the
default container stop, which is still graceful via `stop_grace_period`.

### 2.4 Rollback script

`tools/rollback.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

target=${1:?target version, e.g. v1.0.5}
ROOT=${MAKTABA_ROOT:-/opt/maktaba}
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"

cd "$ROOT"
current=$(curl -s http://localhost:8080/api/system/version | jq -r .tag)
tools/version-jump-guard.sh "$current" "$target" --rollback

echo "==> Checking out $target"
git fetch --tags && git checkout "$target"

echo "==> Pulling images for $target"
MAKTABA_VERSION="$target" $COMPOSE pull
MAKTABA_VERSION="$target" $COMPOSE up -d
```

Crucially: rollback does *not* run a "down migration." The forward-
compat invariant from Story 24.9 means the older binary reads the newer
schema; "rolling back" the schema is unsupported (and dangerous).

### 2.5 Version-jump guard

`tools/version-jump-guard.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
from=${1#v}; to=${2#v}; mode=${3:-upgrade}

# Strip any pre-release suffix (e.g., `1.2.3-rc.4` → `1.2.3`) so the
# numeric comparison treats `1.2.3-rc.4` as the same minor as `1.2.3`
# rather than a major jump (PLAN_REVIEW §22-06). Build metadata after
# `+` is also dropped.
strip_prerelease() {
  local v="$1"
  v="${v%%+*}"
  v="${v%%-*}"
  printf '%s' "$v"
}

from_core=$(strip_prerelease "$from")
to_core=$(strip_prerelease "$to")

IFS='.' read -r fmaj fmin fpat <<<"$from_core"
IFS='.' read -r tmaj tmin tpat <<<"$to_core"

(( fmaj == tmaj )) || { echo "Major-version change unsupported. See UPGRADING.md."; exit 4; }

if (( tmin - fmin > 1 )); then
  echo "Two-minor jump ($from -> $to) refused; upgrade through v${fmaj}.$((fmin+1)).0 first."
  exit 5
fi
if (( tmin - fmin < 0 )) && [[ "$mode" != "rollback" ]]; then
  echo "Backwards minor change requires --rollback explicitly."
  exit 6
fi
```

### 2.6 Migrate-doctor JSON output

Extend `migrate doctor` from Story 22.4:

```go
type DoctorReport struct {
    Version            string                  `json:"version"`
    PendingMigrations  []string                `json:"pending"`
    PerStatement       []StatementTime         `json:"per_statement"`
    LongestStatement   float64                 `json:"longest_statement_seconds"`
    PrePostRowCounts   map[string][2]int       `json:"row_counts_before_after"`
    Warnings           []string                `json:"warnings"`
}
```

`--emit-json` prints the report to stdout; without it, the doctor
prints a human-readable summary. The `tools/upgrade.sh` script
consumes JSON; humans see the summary when running by hand.

### 2.7 Drain handlers

`api/internal/http/admin_drain.go`:

```go
package http

import (
    "context"
    "net/http"
    "sync/atomic"
    "time"
)

type Drainer struct {
    draining   atomic.Bool
    inflight   atomic.Int64
}

func (d *Drainer) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if d.draining.Load() && r.URL.Path != "/api/health" && r.URL.Path != "/admin/drain" {
            w.Header().Set("Connection", "close")
            http.Error(w, "draining", http.StatusServiceUnavailable)
            return
        }
        d.inflight.Add(1)
        defer d.inflight.Add(-1)
        next.ServeHTTP(w, r)
    })
}

func (d *Drainer) Handler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        d.draining.Store(true)
        deadline, _ := strconv.Atoi(r.URL.Query().Get("timeout"))
        if deadline == 0 { deadline = 30 }
        ctx, cancel := context.WithTimeout(r.Context(), time.Duration(deadline)*time.Second)
        defer cancel()
        for d.inflight.Load() > 0 {
            select {
            case <-ctx.Done():
                http.Error(w, "drain timed out with inflight=" + strconv.FormatInt(d.inflight.Load(), 10),
                    http.StatusGatewayTimeout)
                return
            case <-time.After(200 * time.Millisecond):
            }
        }
        w.WriteHeader(http.StatusNoContent)
    }
}
```

SIGTERM also flips `draining` and waits the same way; container stop
calls SIGTERM with `stop_grace_period: 60s` honored by Docker.

### 2.8 Doctor's row-count check

The doctor measures table row counts before and after the migrations
(in the temp DB) and asserts they're identical for tables that
shouldn't lose rows. This catches accidental data-deleting migrations
early:

```go
for _, t := range []string{"videos", "users", "segments", "transcripts"} {
    before := mustCount(ctx, tmpDB, t)
    runMigrationFor(t)
    after := mustCount(ctx, tmpDB, t)
    if before != after {
        report.Warnings = append(report.Warnings,
            fmt.Sprintf("%s: rows %d -> %d (delta %d)", t, before, after, after-before))
    }
}
```

A non-empty `Warnings` is informational, not blocking; large negative
deltas trigger a "review the migration" prompt in the human-readable
output.

### 2.8a Rollback simulator

Schema rollback is forbidden (the rollback flow never invokes
`migrate down`), but the *forward-compat invariant* from Story 24.9
must be exercised: the previous release's binary must boot cleanly
against the new release's schema. A regression here would mean a
rollback drops users into a crash loop.

`tools/rollback-simulator.go` runs in CI on every release-candidate
build:

```go
func TestRollbackSimulator(t *testing.T) {
    ctx := context.Background()

    // 1. Boot a fresh Postgres + apply ALL migrations from the new release.
    db := newFreshPg(t)
    runMigrations(ctx, db, "shared/db/migrations") // current branch

    // 2. Download the previous release's binary (cached by GH artifacts).
    prevBin := downloadPreviousReleaseBinary(t)

    // 3. Boot the previous binary against the migrated DB. The binary
    //    must:
    //      - start without panic,
    //      - serve `/api/health` 200 within 10 s,
    //      - serve a known set of read-only endpoints (videos list,
    //        library show) without 500 — values may differ but must
    //        not crash.
    cmd := exec.CommandContext(ctx, prevBin, "serve", "--config", testConfig)
    cmd.Env = append(os.Environ(), "MAKTABA_DATABASE_URL="+db.URL())
    require.NoError(t, cmd.Start())
    t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM) })

    waitForHealthy(t, "http://127.0.0.1:8080/api/health", 10*time.Second)
    for _, path := range []string{"/api/videos", "/api/libraries"} {
        resp, err := http.Get("http://127.0.0.1:8080" + path)
        require.NoError(t, err)
        require.Less(t, resp.StatusCode, 500, "old binary 500'd on %s after schema upgrade", path)
    }
}
```

CI publishes a clear failure when the simulator trips: ops can hold
the release until the new schema additions are guarded for the older
binary (or until a follow-up old-binary patch ships).

## 3. Test plan

### 3.1 Forward + back (TC1)

| Test | What it pins |
|---|---|
| `TestUpgradeV10ToV11` | Seed a v1.0 fixture; run `tools/upgrade.sh`; the post-upgrade smoke passes; data row counts match. |
| `TestRollbackV11ToV10` | Following the upgrade, run `tools/rollback.sh v1.0.5`; data is intact; the v1.0 binary reads v1.1's schema (forward-compat invariant). |
| `TestNoDownMigrationRun` | The rollback flow never invokes `migrate down`; assert `goose_db_version` is unchanged. |
| `TestRollbackSimulator` | The previous release's binary boots against the new release's schema and serves a documented set of read-only endpoints without 500. CI-blocking for releases. |

### 3.2 Doctor (TC2)

| Test | What it pins |
|---|---|
| `TestDoctorReports` | Synthetic 1 M row migration; doctor emits JSON with `longest_statement_seconds > 30`. |
| `TestUpgradeRefusesLongWithoutAck` | `tools/upgrade.sh` exits 2 when the doctor reports > 30 s; with `ACCEPT_LONG_MIGRATION=1`, runs. |
| `TestDoctorRowCountWarning` | A migration that deletes 100 rows from `videos` triggers a row-count warning. |

### 3.3 Rolling restart (TC3)

| Test | What it pins |
|---|---|
| `TestStreamingRollingDropsLessThan1Pct` | 100 concurrent HLS segment requests during a `streaming` upgrade; the drop-rate is < 1 % (drain holds open requests; new requests bounce to the new container). |
| `TestApiDrainEmpties` | Send 50 long-poll WebSocket subscriptions; trigger drain; all 50 close cleanly within the timeout. |
| `TestPipelineDrainCompletesInflightStage` | A 30-min transcribe in flight; drain refuses new claims; the in-flight stage finishes; the new container picks up new claims. |

### 3.4 Version jump guard

| Test | What it pins |
|---|---|
| `TestTwoMinorJumpRefused` (EC1) | `from=v1.0.0 to=v1.2.0` exits 5 with the documented error. |
| `TestRollbackBackwardsMinorOK` | `--rollback` flag accepts backwards minor jump within one. |
| `TestMajorJumpRefused` | `v1.x → v2.0` is refused without an explicit migration plan (Story 24.9 owns that surface). |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Two-minor jump (EC1) | `version-jump-guard.sh` refuses; the operator runs the upgrade twice — to the intermediate minor, then to the target. | `TestTwoMinorJumpRefused` |
| Custom config path (EC2) | Compose preserves `MAKTABA_CONFIG=/etc/maktaba/api.toml` via env. A schema bump runs `maktaba-api config migrate` inside the new container as part of `tools/upgrade.sh` between drain and start. | `TestConfigSchemaMigrationRuns` |
| Postgres major upgrade (EC3) | Out of scope; documented in `UPGRADING.md` as a Postgres-operator task. The compose Postgres pin is bumped via Renovate; major bumps require a separate runbook. | n/a |
| Drain timeout exceeded | The handler returns 504 with the in-flight count; the upgrade script logs "force-killing svc=$svc inflight=$N" and proceeds. | `TestDrainTimeoutFallback` |
| Image pull mid-upgrade fails | `tools/upgrade.sh` exits at the `pull` step with the prior images still running; no service restarted. | `TestPullFailureNoRestart` |
| Pipeline mid-job | Pipeline drain refuses new claims and waits for the current job to finish; if the job exceeds the drain timeout, the container is killed and the heartbeat-reaper (architecture §7.9) re-claims after the next worker comes up. | `TestPipelineDrainCompletesInflightStage` |
| WebSocket clients during api restart | Cookie + JWT survive the restart (state is in DB); the WS reconnect loop in the web client handles the gap. | `TestWsReconnectOnUpgrade` |
| Migrations fail mid-upgrade | The `up` step is wrapped in a transaction per goose conventions; on failure the schema is unchanged; the new image starts and refuses to serve until migrations complete (boot-time `MAKTABA_AUTO_MIGRATE` retries with backoff). | `TestMigrationFailureRollsBackSchema` |
| Compose lacks `stop_grace_period` | Defaults to 10 s; this story explicitly sets 60 s on api/streaming and 300 s on pipeline. | Compose YAML |
| Rolling restart with mTLS (Story 23.3) | Cert hot-reload happens after the container restart; not a concern for in-flight connections (graceful drain closes them). | n/a |
| Operator runs `compose up -d` without `pull` | The new compose YAML referenced by the upgraded git tag pins the new image tag; without `pull`, compose runs the old image. The script combines pull+up to avoid the foot-gun. | `tools/upgrade.sh` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `docker compose` | v2.27+ | `--no-deps`, `up -d` semantics. |
| `jq` | latest | JSON parsing in shell scripts. |
| `bc` | POSIX | Float comparison. |
| `curl` | POSIX | Health probes. |

## 6. Acceptance checklist

**Upgrade**
- [ ] `tools/upgrade.sh` runs the doctor, pulls images, performs rolling restart via HTTP `/admin/drain` + `/healthz` (no `/usr/local/bin/{drain,healthcheck}` ghost binaries), and smoke-tests.
- [ ] `--backup-before-upgrade` invokes Epic 24.5's `maktaba-backup snapshot` before any image change.
- [ ] Long migrations (> 30 s) require explicit `ACCEPT_LONG_MIGRATION=1`; `bc -l` input is guarded with `${duration:-0}` default.

**Rollback**
- [ ] `tools/rollback.sh` performs a tag checkout + image pull; never invokes `migrate down`.
- [ ] Forward-compat invariant from Story 24.9 covers the data path.
- [ ] `tools/rollback-simulator.go` exercises "old binary against new schema" on every release candidate.

**Drain**
- [ ] api, streaming, and pipeline expose `/admin/drain` and `/healthz` on the admin mux (Story 21.4).
- [ ] SIGTERM flips drain and waits within `stop_grace_period`.

**Guard**
- [ ] Two-minor jumps refused with documented error.
- [ ] Major-version jumps refused outright; documented in UPGRADING.md.
- [ ] Pre-release tag suffixes (`-rc.N`, `+build.metadata`) are stripped before the major/minor comparison.

**Config schema**
- [ ] `maktaba-api config migrate` is owned by this plan (`api/cmd/api/config_migrate.go`); invoked between drain and start.

**Tests**
- [ ] `make dr-drill-upgrade` runs the upgrade + rollback fixture once nightly (Story 24.6 wires this).
