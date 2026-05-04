# Implementation Plan — Story 21.6 Audit Log

> Companion to [story-21-06-audit-log.md](story-21-06-audit-log.md).
> Append-only `audit_log` table (monthly partitions); file mirror;
> trigger-enforced immutability; partitioned retention; views power
> existing endpoints.

## 0. Scope and placement

> **Canonical schema ownership.** This plan is the **sole creator** of
> the `audit_log` table. Architecture §8.6.1 defines the canonical shape;
> the schema in §2 below is a literal implementation of that section.
> Prior plans that created `audit_log` are **superseded**:
> - [plan-09-17](../09-library-management/plan-09-17-library-audit.md)
>   must execute `DROP TABLE IF EXISTS audit_log` and `DROP VIEW IF
>   EXISTS library_audit, security_audit` before re-running.
> - This plan's migration runs **first** in dependency order so any
>   downstream plan that inserts into `audit_log` (Epics 09, 12, 19,
>   21.05/07, 23, 24) compiles against the canonical column set.
> - Epic 12 plan-12-10 wrote `category='device'`; the CHECK enum below
>   includes `'device'` (and `'session'`, `'job'`) so existing inserts
>   continue to succeed.

| Concern | Decision |
|---|---|
| Schema owner | This plan is the sole creator of `audit_log` (architecture §8.6.1). Prior plans (plan-09-17) are superseded; `library_audit`/`security_audit` collapse into views. |
| File mirror | `/var/maktaba/audit/audit.log` JSON-Lines. Only used when DB unreachable; replayed on reconnect. |
| Append-only | `BEFORE UPDATE OR DELETE` trigger raises exception. Retention via `DETACH PARTITION` + `DROP TABLE`. |
| Read audit | Every API read against audit emits a `data` audit row (read-audit). |
| `error_id` linkage | Stored in `payload->>'error_id'` (NOT a top-level column). plan-21-05 is the producer; plan-21-07 reads via `payload->>'error_id'` and `processing_jobs.last_error_id`. |
| `target_id` typing | `TEXT` (handles UUIDs, library hashes, video ids, device ids, session ids). Consumers casting to UUID must qualify with a `target_kind` filter or use `payload->>'target_uuid'`. |

## 1. Project layout

```
shared/db/migrations/
├── 00xx_audit_log.sql                # table + partition mgmt + trigger
└── 00xx_audit_log_views.sql

shared/audit/
├── go/
│   ├── emit.go
│   ├── reader.go                     # POST-emit when /api/security/audit is read
│   ├── mirror.go                     # file mirror
│   ├── replayer.go                   # mirror → DB on reconnect
│   ├── partitions.go                 # monthly create/detach
│   ├── retention.go                  # scheduled task
│   └── tests/
└── py/
    ├── emit.py
    └── tests/

api/internal/handlers/
├── library_audit.go                  # GET /api/libraries/{id}/audit
└── security_audit.go                 # GET /api/security/audit

cmd/maktaba-admin/
└── audit_drop_partition.go           # CLI; emits its own audit row
```

## 2. Schema

The schema below is the literal canonical shape from architecture §8.6.1.
Field-by-field rationale for the CHECK enum:

| Category   | Owner / producer                                        |
|------------|---------------------------------------------------------|
| `library`  | Epic 09 (library lifecycle, library_audit endpoint)     |
| `security` | Epic 09/23 (auth-adjacent events not under `auth`)      |
| `device`   | Epic 12 (device pairing/revocation)                     |
| `admin`    | Admin actions (Story 21.6 retention CLI, Story 23 ops)  |
| `auth`     | Epic 10/23 (login, refresh, lockout, rate-limit events) |
| `data`     | Read-audit rows + Story 24 corruption events            |
| `config`   | Settings changes (Story 19/23 admin-config writes)      |
| `keys`     | Epic 10 RS256 keys, Story 23 key rotation               |
| `job`      | plan-21-07 job pipeline (`event='job.error'` etc.)      |

```sql
-- 00xx_audit_log.sql
-- Canonical schema per architecture §8.6.1. SOLE CREATOR of audit_log.
-- Older plans that created the table (plan-09-17) are superseded and
-- must DROP TABLE IF EXISTS audit_log before re-running.

CREATE TABLE audit_log (
    id           BIGSERIAL,
    category     TEXT NOT NULL CHECK (category IN (
                   'library','security','device','admin',
                   'auth','data','config','keys','job'
                 )),
    event        TEXT NOT NULL,
    actor_user   UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    actor_ip     INET NULL,
    actor_ua     TEXT NULL,
    target_kind  TEXT NULL,
    target_id    TEXT NULL,                          -- TEXT, not UUID; handles every id shape
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    dedupe_key   TEXT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    clock_source TEXT NOT NULL DEFAULT 'db',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;
-- bootstrap one month
CREATE TABLE audit_log_y2026m05 PARTITION OF audit_log
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE INDEX audit_log_category_created_at_idx ON audit_log (category, created_at DESC);
CREATE INDEX audit_log_actor_user_created_at_idx ON audit_log (actor_user, created_at DESC) WHERE actor_user IS NOT NULL;
CREATE INDEX audit_log_target_idx ON audit_log (target_kind, target_id, created_at DESC);
CREATE INDEX audit_log_dedupe_key_idx ON audit_log (dedupe_key) WHERE dedupe_key IS NOT NULL;
-- error_id resolves to payload->>'error_id'; helper expression index
-- so plan-21-07 'last 50 errors' lookups stay fast.
CREATE INDEX audit_log_error_id_idx ON audit_log ((payload->>'error_id'))
  WHERE payload ? 'error_id';

-- Append-only trigger
CREATE OR REPLACE FUNCTION audit_log_block_mutate() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END $$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_block_mutate();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_block_mutate();

-- Views consumed by existing endpoints. NOTE: views cannot carry their
-- own indexes; query planners use the underlying partition indexes.
-- plan-21-07 EXPLAIN expectations therefore target
-- `audit_log_y<NNNN>m<MM>` partitions, not the view names.
CREATE VIEW audit_log_security AS
    SELECT * FROM audit_log WHERE category IN ('auth','admin','keys','security');

CREATE VIEW audit_log_library AS
    SELECT * FROM audit_log WHERE category = 'library';
```

**Primary-key choice (`BIGSERIAL` vs UUIDv7).** `BIGSERIAL` allocates a
*per-partition* sequence under PostgreSQL's declarative partitioning,
so cross-partition collisions are not possible (each partition is its
own sequence-bearing relation; rows are unique per (id, created_at)
which is the partition key + sequence). If a future deployment shards
audit emission across multiple writers (e.g., per-region replicas),
this plan switches to UUIDv7 (`uuid_generate_v7()`) — until then,
`BIGSERIAL` is retained for index density.

## 3. Emit (Go)

```go
// shared/audit/go/emit.go
type Event struct {
    Category   string
    Event      string
    ActorUser  *uuid.UUID
    ActorIP    *netip.Addr
    ActorUA    string
    TargetKind string
    TargetID   string
    Payload    map[string]any
}

type Emitter struct{ db *sql.DB; mirror *Mirror }

func (e *Emitter) Emit(ctx context.Context, ev Event) {
    payload, _ := json.Marshal(ev.Payload)
    _, err := e.db.ExecContext(ctx, `
        INSERT INTO audit_log
            (category, event, actor_user, actor_ip, actor_ua, target_kind, target_id, payload, clock_source)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,'db')`,
        ev.Category, ev.Event, ev.ActorUser, ev.ActorIP, ev.ActorUA, ev.TargetKind, ev.TargetID, payload)
    if err == nil { return }
    // EC1: DB unreachable — write to mirror
    e.mirror.Write(ev)
}
```

Helpers:

```go
func AuthEvent(ctx context.Context, e *Emitter, name string, user *uuid.UUID, ip netip.Addr, ua string, payload map[string]any) {
    e.Emit(ctx, Event{Category: "auth", Event: name, ActorUser: user, ActorIP: &ip, ActorUA: ua, Payload: payload})
}
```

## 4. File mirror

```go
// shared/audit/go/mirror.go
type Mirror struct{ path string; mu sync.Mutex }

func (m *Mirror) Write(ev Event) error {
    m.mu.Lock(); defer m.mu.Unlock()
    f, err := os.OpenFile(m.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
    if err != nil { return err }
    defer f.Close()
    line := withClockSource(ev, "wall")            // EC2
    return json.NewEncoder(f).Encode(line)
}
```

Replayer:

```go
// shared/audit/go/replayer.go
func (r *Replayer) ReplayLoop(ctx context.Context) {
    for {
        if err := r.tryReplay(ctx); err != nil { time.Sleep(30*time.Second); continue }
        time.Sleep(5*time.Minute)
    }
}

func (r *Replayer) tryReplay(ctx context.Context) error {
    f, err := os.Open(r.path); if err != nil { return nil }
    defer f.Close()
    sc := bufio.NewScanner(f)
    var replayed int
    for sc.Scan() {
        var ev Event
        if err := json.Unmarshal(sc.Bytes(), &ev); err != nil { continue }
        if err := r.emit.Emit(ctx, ev); err != nil { return err }
        replayed++
    }
    if replayed > 0 {
        _ = os.Truncate(r.path, 0)
    }
    return nil
}
```

## 5. Partition mgmt

```go
// shared/audit/go/partitions.go
func EnsureNextMonthPartition(ctx context.Context, db *sql.DB) error {
    next := time.Now().UTC().AddDate(0,1,0)
    name := fmt.Sprintf("audit_log_y%04dm%02d", next.Year(), int(next.Month()))
    from := time.Date(next.Year(), next.Month(), 1, 0,0,0,0, time.UTC)
    to   := from.AddDate(0,1,0)
    _, err := db.ExecContext(ctx, fmt.Sprintf(`
        CREATE TABLE IF NOT EXISTS %s PARTITION OF audit_log FOR VALUES FROM ('%s') TO ('%s')`,
        name, from.Format("2006-01-02"), to.Format("2006-01-02")))
    return err
}
```

Run daily via scheduled task.

## 6. Retention with audit-of-the-drop

```go
// shared/audit/go/retention.go
type Policy struct{ Category string; Months int }

var Default = []Policy{
    {Category: "auth", Months: 6},
    {Category: "library", Months: 12},
    {Category: "admin", Months: 12},
    {Category: "data", Months: 12},
    {Category: "config", Months: 12},
    {Category: "keys", Months: 36},
}
```

Retention runs monthly. For each policy:

1. Compute boundary date.
2. Query partitions to detach (those whose `to_date <= boundary`).
3. For each: `BEGIN; ALTER TABLE audit_log DETACH PARTITION %s; DROP TABLE %s; COMMIT;`
4. Emit a `data` audit row recording the partition drop with row-count and policy.

CLI override:

```go
// cmd/maktaba-admin/audit_drop_partition.go
RunE: func(cmd *cobra.Command, args []string) error {
    name := args[0]
    rows, _ := db.QueryRow(`SELECT pg_class_size('public.'||$1)`, name).Scan(&sz)
    if !forceFlag { return errors.New("--force required") }
    _, err := db.Exec(fmt.Sprintf(`ALTER TABLE audit_log DETACH PARTITION %s`, name))
    if err != nil { return err }
    _, err = db.Exec(fmt.Sprintf(`DROP TABLE %s`, name))
    audit.Emit(ctx, Event{Category: "data", Event: "audit_partition_drop_admin",
        Payload: map[string]any{"partition": name, "size_bytes": sz}})
    return err
}
```

## 7. View-backed endpoints

```go
// api/internal/handlers/library_audit.go
func (h *Handler) ListLibraryAudit(w http.ResponseWriter, r *http.Request) {
    libID := chi.URLParam(r, "id")
    rows, err := h.db.QueryContext(r.Context(), `
        SELECT id, created_at, event, actor_user, actor_ip, payload
          FROM audit_log_library
         WHERE target_kind='library' AND target_id=$1
         ORDER BY created_at DESC LIMIT 200`, libID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    defer rows.Close()
    out := []AuditRow{}
    for rows.Next() { /* scan */ }
    audit.Emit(r.Context(), audit.Event{Category: "data", Event: "library_audit_read",
        ActorUser: userFrom(r), TargetKind: "library", TargetID: libID,
        Payload: map[string]any{"count": len(out)}})    // EC3 read-audit
    json.NewEncoder(w).Encode(out)
}
```

## 8. Test cases

### TC1 — Append-only
```sql
UPDATE audit_log SET event='hax' WHERE id=1;          -- expects exception
DELETE FROM audit_log WHERE id=1;                     -- expects exception
```

### TC2 — Login flow
Failed login `POST /api/auth/login` with wrong password. Assert audit row: `category=auth, event=login_failed, payload.username='attempted-name'`. Assert: `payload` does NOT contain `password`.

### TC3 — Retention
Pre-create partition `audit_log_y2024m04` with rows. Run retention with `auth=6 months` policy on May 2026. Assert: partition detached, dropped; an audit row `event=audit_partition_drop` with `partition=audit_log_y2024m04` is present.

### TC4 — View parity
Insert rows with categories `auth, admin, keys, library, data, config`. `GET /api/libraries/{id}/audit` returns only `library`. `GET /api/security/audit` returns only `auth, admin, keys`. Both endpoints' EXPLAIN plans use the indexed view (no seq scan).

### EC1 — DB outage
Stop DB. Trigger an `auth.login_failed` event. Mirror file appends a JSON line with `clock_source=wall`. Restart DB. Replayer runs; line replayed; mirror truncated.

### EC3 — Read-audit
`GET /api/security/audit` once. Assert a new `data.security_audit_read` row exists naming the requesting user.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 DB outage | story | File mirror → replayer. |
| EC2 wall-clock | story | `clock_source` column. |
| EC3 read-audit | story | Endpoints emit `data.*_read`. |
| Partition not yet created | impl | `EnsureNextMonthPartition` daily; `audit_log_default` catches gaps. |
| Trigger edits | impl | Migration adds; if dropped, schema audit lint catches. |

## 10. Configuration

```yaml
audit:
  mirror_path: /var/maktaba/audit/audit.log
  retention:
    auth_months: 6
    library_months: 12
    admin_months: 12
    data_months: 12
    config_months: 12
    keys_months: 36
  partition_create_lookahead_days: 7
```

## 11. Dependencies

- Story 21.1 (logger).
- Story 23.1, 23.6 (auth events; confirmation tokens for `purge=true` audited).
- Story 19.5 (migration safety).
- Story 9.x library lifecycle (emit `library.*` rows).
- Story 21.5 (`error_id` may appear in payload of admin events).
