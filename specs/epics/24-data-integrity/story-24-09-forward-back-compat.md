# Story 24.9 — Forward and backward compatibility

State produced by version N must be readable by N+1 (and reasonably
by N-1 across one minor version), so upgrades and rollbacks
([Epic 22.6](../22-devops/story-22-06-upgrade-rollback.md)) don't
corrupt data.

## Acceptance criteria

- AC1. Schema changes follow the documented playbook: add column
  nullable → backfill → set NOT NULL in a later release. A single
  release never introduces "breaking" reads of old rows.
- AC2. Generated artifact formats (segment JSON, sidecars) carry a
  `schema_version` field; readers tolerate higher minor versions
  by ignoring unknown fields.
- AC3. Cache key prefixes include the platform major version; a
  major bump invalidates caches automatically.
- AC4. A "forward-compat" test loads fixtures captured from
  previous versions and asserts they still work in the current
  version.

## Test cases

- TC1. Old dump load: a v1.0 `pg_dump` restores into a v1.2
  schema; migrations run; smoke test passes.
- TC2. Old sidecar parse: a `schema_version=1` segment JSON file is
  parsed by the current version with the documented field-default
  behavior.
- TC3. Cache invalidation on major bump: simulating v2.0, v1.x
  cache entries are ignored; new entries are written under the
  v2 prefix.

## Edge cases

- EC1. A v1.x client connecting to a v1.(x+1) server — supported
  per Epic 22.6; a v1.x client connecting to a v2.0 server is
  refused with a clear "incompatible major version" message.
- EC2. `schema_version` missing on an old artifact — reader treats
  it as `1`; documented.
- EC3. Lossy migration (rare; e.g., dropping a deprecated field) —
  documented in CHANGELOG and the migration script archives the
  data to a `removed_data_v{n}` JSON file for forensics.
