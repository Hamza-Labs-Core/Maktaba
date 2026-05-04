# Implementation Plan — Story 24.9 Forward and backward compatibility

> Companion to [story-24-09-forward-back-compat.md](story-24-09-forward-back-compat.md).
> Story states *what* and *why*; this plan states *how*.
> Pairs with the upgrade/rollback flow from
> [Story 22.6](../22-devops/plan-22-06-upgrade-rollback.md) and the
> **migration discipline from
> [plan-22-04 (Database Migrations)](../22-devops/plan-22-04-database-migrations.md)**
> — schema discipline (add nullable → backfill → set NOT NULL) is owned
> by plan-22-04's CI lint and is **not** re-implemented here. This plan
> covers the artifact-format and cache-key dimensions of compat.
>
> **Forward-compat fixtures are non-existent at v1.0 cut.** The
> `tests/forward_compat/<v>/` directories are populated as each
> subsequent minor ships; reviewers should not expect them on day one.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Schema discipline | "Add nullable → backfill → set NOT NULL in a later release." Enforced by **plan-22-04**'s migration lint (cross-linked by name; not re-implemented here). |
| Artifact format | Every generated **JSON manifest** artifact (`segments.json`, `vtt_extras.json`, `sprites_manifest.json`) carries a top-level `schema_version: <int>`. Readers tolerate higher minor versions by ignoring unknown fields; loud-fail on a major version they don't recognize. |
| SRT/VTT excluded | Subtitle artifacts (`*.srt`, `*.vtt`) are **W3C/SubRip standard formats** with their own version semantics; we do not append a `schema_version` to them (it would not validate). Compatibility for these artifacts is governed by the spec versions; the `vtt_extras.json` sidecar (a JSON manifest) carries the schema version that matters for our own additions. |
| gRPC field-number stability | Per §3 below: never reuse a proto field number; deprecate-don't-delete. The proto lint (plan-22-04 ecosystem) catches reuse on rebuild. |
| Cache key prefix | Includes `MAJOR` of the platform version (e.g., `v1:hls:<hash>`). A major bump invalidates caches; readers ignore the older prefix. |
| WebSocket close-code | `4001` for incompatible-major; **constant pinned in client SDKs** (web, mobile) so all clients recognize the same value. |
| Forward-compat fixture suite | `tests/forward_compat/` contains snapshots from previous versions; CI runs the current code against them. **Empty at v1.0 cut**; populated incrementally. |
| Forensic archive dir | `var/maktaba/forensic/` — **owned by `maktaba:maktaba 0755`** so the API process can write archive files; readable by ops via group membership. The directory is created at first lossy-migration with `MkdirAll(... 0o755)` and `chown` to the `maktaba` system user. |
| Out of scope | `pg_dump` cross-version restore mechanics (24.5); upgrade/rollback wall-clock (22.6); migration lint (plan-22-04). |

## 1. Architecture diagram

```
                  ┌──────────────────────┐
   write artifact │ generators           │
                  │   schema_version: N  │ ← bump on breaking change
                  └──────────────────────┘
                            │
                            ▼
                  on-disk JSON, VTT
                            │
                            ▼
                  ┌──────────────────────┐
   read artifact  │ readers              │
                  │   if v == expected   │ → read normally
                  │   if v > expected    │ → ignore unknown fields
                  │   if v < expected    │ → migrate-on-read or error
                  └──────────────────────┘

   tests/forward_compat/  v1.0/ v1.1/ … fixtures
        │
        ▼
   the current build asserts each fixture parses + smoke-tests pass
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/compat/schema_version.py` | `read_versioned(reader, expected_max)` — central reader. |
| `pipeline/src/maktaba_pipeline/compat/migrate_on_read.py` | Per-artifact upgraders (e.g., v1 → v2 for `segments.json`). |
| `api/internal/cache/keys.go` | Adds the platform-major prefix everywhere cache keys are formed. |
| `tests/forward_compat/v1.0/`, `v1.1/`, … | Captured fixture snapshots. |
| `tests/forward_compat/runner.py` | Loads each fixture and runs a smoke test against the current code. |
| `tools/major-version-cache-bump.sh` | Helper for the `v2` cut (sweeps cache directories). |

### 2.2 Modified files

| Path | Change |
|---|---|
| Every artifact writer | Emits `schema_version` at top. |
| Every artifact reader | Routes through `schema_version.read_versioned`. |
| `api/cmd/api/main.go` | At boot, verifies the connecting client's version on the WebSocket and refuses incompatible majors per EC1. |

### 2.3 Schema version reader

`schema_version.py`:

```python
from typing import Any, Callable, TypeVar

T = TypeVar("T")

CURRENT = {
    "segments.json": 2,
    "vtt_extras.json": 1,
    "sprites_manifest.json": 1,
}

class IncompatibleMajor(RuntimeError):
    """Reader cannot understand this artifact version."""

def read_versioned(
    artifact_kind: str,
    raw: dict[str, Any],
    parser: Callable[[dict, int], T],
) -> T:
    """Validate schema_version on the artifact and dispatch to the
    appropriate parser.

    Defaults to schema_version=1 when missing (EC2).
    """
    v = raw.get("schema_version", 1)
    expected = CURRENT[artifact_kind]
    if v > expected:
        # Forward-compat: tolerate higher versions by stripping unknown
        # fields and routing through the current parser. The format
        # promise is "minor bumps add fields; major bumps require a
        # migrate-on-read step."
        if _is_minor_bump(artifact_kind, v):
            return parser(raw, expected)
        raise IncompatibleMajor(
            f"{artifact_kind}: schema {v} is too new (current={expected})")
    if v < expected:
        # Backward-compat: upgrade in memory before parsing.
        return parser(migrate_on_read.upgrade(artifact_kind, raw, v, expected), expected)
    return parser(raw, expected)
```

`_is_minor_bump` is a small declarative table that lists which version
deltas are minor (additive) vs major (breaking). Bumps require a
deliberate update.

### 2.4 Migrate-on-read

`migrate_on_read.py`:

```python
# Pure transformations. Each upgrader takes the raw dict and returns
# the same artifact at the next version up.
UPGRADERS = {
    ("segments.json", 1, 2): _segments_v1_to_v2,
}

def upgrade(kind, raw, from_v, to_v):
    cur = raw
    while from_v < to_v:
        f = UPGRADERS.get((kind, from_v, from_v + 1))
        if f is None:
            raise IncompatibleMajor(f"no upgrader for {kind} v{from_v} → v{from_v+1}")
        cur = f(cur)
        from_v += 1
    return cur

def _segments_v1_to_v2(raw):
    """v1 stored a single `text` field; v2 splits into `text` and
    `language` (defaulting to "und" when missing)."""
    raw["schema_version"] = 2
    for s in raw.get("segments", []):
        s.setdefault("language", "und")
    return raw
```

The upgraders never write to disk. The on-disk file remains v1 until a
write path triggers a v2 emission.

### 2.5 Cache key prefix

`api/internal/cache/keys.go`:

```go
import "maktaba/internal/version"

func MajorPrefix() string {
    // version.Tag is "v1.2.0"; major is 1.
    // Sanity: an empty or malformed tag is a build-time error — we
    // panic loudly rather than silently emit "v" or "v0", which would
    // collide cache keys across builds.
    tag := strings.TrimSpace(strings.TrimPrefix(version.Tag, "v"))
    if tag == "" {
        panic("version.Tag is empty — build-time ldflag missing")
    }
    parts := strings.SplitN(tag, ".", 2)
    if parts[0] == "" {
        panic("version.Tag has no major component: " + version.Tag)
    }
    return "v" + parts[0]
}

func HLSKey(videoID, profile string) string {
    return fmt.Sprintf("%s:hls:%s:%s", MajorPrefix(), videoID, profile)
}

func SpriteKey(videoID string) string {
    return fmt.Sprintf("%s:sprite:%s", MajorPrefix(), videoID)
}

func EmbeddingKey(videoID string) string {
    return fmt.Sprintf("%s:emb:%s", MajorPrefix(), videoID)
}
```

A v2 cut yields `v2:hls:...` keys; the on-disk LRU cache (Epic 8)
ignores the older prefix; `tools/major-version-cache-bump.sh` sweeps
v1 entries:

```bash
#!/usr/bin/env bash
set -euo pipefail
old_major=$1
find /var/maktaba/cache -maxdepth 2 -name "${old_major}:*" -delete
```

### 2.6 Forward-compat fixture suite

`tests/forward_compat/v1.0/`:

- `pg_dump.dump` — captured DB dump from v1.0.
- `segments-sample.json` — schema_version=1 example.
- `expected.json` — the parsed-and-rendered representation our current
  code should produce.

`tests/forward_compat/runner.py`:

```python
import json
from pathlib import Path

ROOT = Path(__file__).parent

def test_each_fixture():
    for ver in sorted(ROOT.iterdir()):
        if not ver.is_dir(): continue
        # 1. Restore the dump into a fresh DB; run migrations forward.
        restore(ver / "pg_dump.dump")
        run_migrate_up()
        # 2. Smoke: catalog endpoints respond; counts match expected.
        smoke(ver / "smoke-expected.json")
        # 3. Parse the sample artifact through the versioned reader.
        raw = json.loads((ver / "segments-sample.json").read_text())
        parsed = read_versioned("segments.json", raw, parse_segments)
        expected = json.loads((ver / "expected.json").read_text())
        assert parsed == expected, f"fixture {ver.name} mismatch"
```

The runner is invoked from `make test-integration`. New versions
deposit a fresh fixture in `tests/forward_compat/<ver>/`.

### 2.7 Major-version client refusal

`api/cmd/api/main.go` adds:

```go
// On WebSocket upgrade, the client sends its own version in the
// connect frame. If the major mismatches, refuse with a typed close
// frame.
ws.OnConnect(func(c *Conn) error {
    clientMaj := c.MetaInt("client_major")
    serverMaj := majorFromTag(version.Tag)
    if clientMaj != serverMaj {
        c.CloseWithReason(4001, fmt.Sprintf(
            "incompatible-major: client=v%d server=v%d", clientMaj, serverMaj))
        return errors.New("major mismatch")
    }
    return nil
})
```

The web client's WS reconnect loop displays a "this version of the app
is too old / too new — please refresh" message on close-code 4001.
Mobile/desktop apps surface the equivalent dialog.

`web/src/lib/ws/constants.ts` (and the equivalent file in each mobile
SDK) pins the close-code constant:

```typescript
// All clients MUST use this constant — never a literal 4001 inline —
// so future renames stay in sync.
export const WS_CLOSE_INCOMPATIBLE_MAJOR = 4001 as const;
```

### 2.8 gRPC field-number stability

The internal gRPC contract between `api` and `pipeline` (architecture
§9.x) follows the standard proto3 evolution rules:

- **Never reuse a field number.** When a field is removed, mark it
  `reserved <n>;` in the proto definition. The proto lint
  (`tools/proto-lint`, owned by plan-22-04's ecosystem) fails CI if a
  reused number is detected by parsing the proto's reserved blocks.
- **Deprecate, don't delete.** Adding `[deprecated = true]` on an
  existing field signals removal in the next major; readers must keep
  parsing it for one full minor cycle.
- **No type changes.** Once `int32`, always `int32` for that field
  number; a type change is a major bump.
- **Wire-compat tests.** A `tests/proto_compat/` set holds serialized
  payloads from previous minors; the current code must successfully
  parse all of them (forward-compat) and the previous code must parse
  current emits (backward-compat) as long as the major hasn't bumped.

The proto lint is fast (parses the .proto AST) and runs on every CI
build, not just release.

## 3. Test plan

### 3.1 Old dump load (TC1)

| Test | What it pins |
|---|---|
| `TestRestoreV10IntoCurrent` | Drop into the current schema; restore `tests/forward_compat/v1.0/pg_dump.dump`; run `migrate up`; smoke. |
| `TestEveryDocumentedVersionFixtureLoads` | Every `tests/forward_compat/v*/` directory must load and pass smoke; missing fixture files fail CI with a descriptive message. |

### 3.2 Old sidecar parse (TC2)

| Test | What it pins |
|---|---|
| `TestParseSegmentsV1` | A `schema_version=1` sample is read with v1→v2 migrate-on-read; the resulting struct matches the `expected.json` reference. |
| `TestParseSegmentsMissingVersion` (EC2) | A file without `schema_version` is treated as v1 per the documented default. |
| `TestParseSegmentsTooNewMinor` | A `schema_version=2` file with extra fields parses; unknown fields are dropped with a debug log. |
| `TestParseSegmentsTooNewMajor` | A `schema_version=99` file raises `IncompatibleMajor` (the bump table doesn't list 2→99 as minor). |

### 3.3 Cache invalidation on major bump (TC3)

| Test | What it pins |
|---|---|
| `TestCacheKeysCarryMajorPrefix` | Built with `version.Tag=v1.2.0` → keys start `v1:`. Bumped to `v2.0.0` → keys start `v2:`. |
| `TestV1CacheIgnoredOnV2Build` | Pre-populate v1 entries; build under v2; reads miss; writes go under v2:. |
| `TestSweeperRemovesOldMajor` | `tools/major-version-cache-bump.sh v1` removes only `v1:*` entries. |
| `TestMajorPrefixEmptyStringPanics` | `version.Tag=""` → `MajorPrefix()` panics with the documented message; CI's release build sets the ldflag so this only fires on broken local builds. |
| `TestMajorPrefixNoMajorComponent` | `version.Tag="v"` → panic with "no major component". |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| v1.x → v1.x+1 client (EC1) | Per Story 22.6, supported across one minor; the WS's major check passes. | `TestMinorClientCompatible` |
| v1.x → v2.0 client | Refused with close-code 4001 + clear UI message. | `TestMajorClientRefused` |
| Missing `schema_version` (EC2) | Default to 1; documented; readers tolerate. | `TestParseSegmentsMissingVersion` |
| Lossy migration (EC3) | Documented in CHANGELOG. The migration script archives removed columns to `removed_data_v{n}.json` under `var/maktaba/forensic/`. | `TestLossyMigrationArchive` |
| Adding a NOT NULL column without backfill | Caught by 22.4's migration lint; cannot land. | n/a (22.4) |
| Future-dated `schema_version` from a malicious client | Treated like any other version mismatch; the IncompatibleMajor error refuses to load; doesn't crash. | `TestNegativeOrAbsurdSchemaVersion` |
| Fixture rot | `tests/forward_compat/runner.py` requires every version directory to remain green. When a v1.0 feature is removed, the fixture's expected output must be updated and committed in the same PR. | `TestFixtureExpectedDriftCaught` |
| Cache prefix length | The major prefix adds 3 bytes per key; negligible. | n/a |
| Streaming session JWT version skew | Streaming verifies JWTs by `kid`, not by version; cross-major JWT forgery is bounded by the JWKS rotation policy (Story 23.1). | n/a |
| Two majors live during a rolling deploy | Not supported. The compose stack rolls all services together for major bumps. Documented in 22.6. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `json` | stdlib | Read/write artifact JSON. |
| Story 22.4's migration lint | already | Schema discipline. |
| Story 22.6's upgrade flow | already | One-minor-at-a-time invariant. |

## 6. Acceptance checklist

**Schema discipline**
- [ ] Add-nullable → backfill → set-NOT-NULL pattern enforced by 22.4 lint.

**Artifact format**
- [ ] Every artifact has `schema_version`.
- [ ] Readers route through `read_versioned`.
- [ ] Migrate-on-read upgraders provided per documented bump.

**Cache**
- [ ] Cache keys carry platform major prefix.
- [ ] Major bump sweeper helper exists.

**Forward-compat suite**
- [ ] `tests/forward_compat/<v>/` per past minor.
- [ ] `make test-integration` runs the suite.

**Client compat**
- [ ] WS rejects incompatible major with close-code 4001.
- [ ] UI surfaces the "incompatible major" message.
