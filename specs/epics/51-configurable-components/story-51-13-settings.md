# Story 51.13 — Component settings: validated, versioned, kept

> Epic 51 · Phase 2 · Depends on: 51.08 · Needed by: 51.16 (purge)

## Why

Settings stay where they are (H7): `app_settings` keys `rules.<section>`, one schema per
section in `pipeline/src/maktaba_pipeline/rules.py` `SECTIONS` and the `*_settings.py`
modules, generated into `api/internal/handlers/rules/schema.json`. The API already checks each
field against that schema on `PATCH /api/rules/{section}` (`rules.go` `Patch`). Three things
are missing for components that come and go:

1. **No version.** When a release renames or reshapes a field, the old stored value is read
   field by field and a field that no longer fits is skipped with a note
   (`rules.config`: "one bad edit never stops a stage"). The stage runs on the default for it,
   and nobody is told. For a component turned off for months and turned on after two
   updates, that is a silent change of behaviour.
2. **No owner.** Nothing says which section belongs to which component, so the Components page
   can't link a component to its form, and nothing protects a component's settings when it is
   removed.
3. **No clean purge.** Removing a component should keep its settings by default, and delete
   them only when the admin asks.

## Description

### 1. Owners

The catalog's `settings` list (51.01) names the sections each component owns
(`settings = ["rules.transcription"]`). The `catalog` job adds two checks: every name is a key
of `SECTIONS`, and a section has at most one owner. A section no component names belongs to
`core.*` (always on). `GET /api/rules` adds `component` (the owner id or null) and `version` to
each section, so the Rules page can say "Part of Transcription (off)" and the Components page
(51.10) can link `/settings/rules#transcription`.

### 2. A version per section

- Each config class declares `SCHEMA_VERSION = (major, minor)`, default `(1, 0)`. A test fails
  when a field is removed or its kind changes without a major bump. It compares with
  `schema.json` at the last release tag (`git show <tag>:api/internal/handlers/rules/schema.json`
  in `tools/ci/rules_schema_versions.py`; the lint job fetches tags).
- **Minor** bump: only new fields with defaults. Old values load as they are.
- **Major** bump: the section's module ships `migrate_v<N>_to_v<N+1>(old: dict) -> dict`,
  registered with `settings.register_migration(Config, N, fn)`. Migrations chain (1→2→3).
- `rules.schema()` writes `"version": [major, minor]` per section into `schema.json`; the
  existing `test_the_api_schema_is_current` keeps the file current.
- The stored version sits in its own key, **`rules_version.<section>`** =
  `{"major":1,"minor":0}`. A missing key means `1.0`. It is a separate key so the stored
  section object stays exactly as older releases read it, and `LIKE 'rules.%'` (the API's
  `loadAll`) does not pick it up.

### 3. Migrating, once, on the home

The home's pipeline runner (core, always on) checks every section at start and after each
update, whatever the owning component's state:

1. Stored major = code major: nothing to do. Stored minor older: write the new minor.
2. Stored major older: run the chain on the stored object, validate the result with
   `settings.apply`, then write the new object, the new `rules_version` (with
   `"previous": {"major": N, "value": {…}}` kept for support and rollback) and an `audit_log`
   row (`category = 'components'`, `action = settings.migrate`) in **one transaction**.
3. A migration raises, or its result does not validate: nothing is written. The version key
   gets `"failed": "<one-line error>"`, and the owning component's runners report it
   **Failed** with reason **`settings_invalid`** (`params: {section, error}`). The component
   does not start on defaults. Components that don't own the section are not touched.
4. Stored major newer than the code's (a release rollback): the older release has no version
   logic and reads field by field, as today. 51.18's rehearsal checks that this keeps the
   component running.

Nodes never migrate; they read what the home wrote.

### 4. Validation on write

`PATCH /api/rules/{section}` keeps its per-field checks and adds:

- the `version` from `schema.json` is written to `rules_version.<section>` in the same
  transaction as the value;
- while the stored major is older and not yet migrated, a `PATCH` answers
  `409 settings_need_migration` (seconds after an update, until the pipeline runner has run);
- while the version key says `failed`, a `PATCH` must send the whole section
  (`"replace": true`); it is validated in full against the current schema and replaces the old
  value. That clears `failed`, and the runners leave *Failed* on their next pass.

Roles are unchanged: admins only (`rules.go` `adminOnly`), with the role test extended to the
new body field and a cloud-linked play sign-in.

### 5. Kept on disable and remove; purged on request

- Disabling, holding or removing a component never touches `app_settings`. The settings are
  there when it comes back (Nextcloud's `--keep-data`).
- **`POST /api/components/{id}/settings/reset`** (admin-only) deletes the component's
  `rules.<section>` and `rules_version.<section>` keys and writes one audit row per section
  with the old value in `before`. 51.16's `remove?purge=true` calls the same function, then
  deletes the component's data.
- The web asks first: "Reset Transcription's settings to the defaults? Your changes to 7
  settings will be lost." (en + ar), and the CLI asks the same unless `--yes`
  (`maktaba-server components reset-settings <id>`).

### 6. Words

New strings, English and Arabic together in `web/src/i18n/en.json` / `ar.json`:
`rules.section.partOf` (Part of %s / جزء من %s), `rules.section.migrateFailed` (These settings
could not be moved to this release's format. Check them and save. / تعذّر نقل هذه الإعدادات إلى
صيغة هذا الإصدار. راجعها ثم احفظها.), the reset dialog, and the `settings_invalid` reason words
(51.01 already lists the code).

## Acceptance criteria

- **Given** `rules.transcription` stored at 1.0 and a release where `TranscriptionConfig` is
  2.0 with `migrate_v1_to_v2`,
  **when** the home's pipeline starts,
  **then** the stored value is migrated, `rules_version.transcription` is `2.0` with the old
  value under `previous`, and one audit row is written.
- **Given** the migration raises,
  **when** the pipeline starts,
  **then** nothing is written to `rules.transcription`, transcription is *Failed* with
  `settings_invalid`, and other components run.
- **Given** that failure,
  **when** an admin saves the full section with `replace: true` and valid values,
  **then** transcription leaves *Failed* within 30 s.
- **Given** an admin turns transcription off and later back on,
  **when** they open its settings,
  **then** every changed setting is still there.
- **Given** an admin resets Transcription's settings,
  **when** they confirm,
  **then** both keys are gone, the audit row holds the old value, and the form shows defaults.
- **Given** a field's kind changes without a major bump,
  **when** CI runs,
  **then** the schema version test fails naming the section and field.
- **Given** no sign-in, a viewer or a cloud-linked play sign-in,
  **when** they call `PATCH /api/rules/{section}` or the reset route,
  **then** `401` (no sign-in) or `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Py) | fake config 2.0 with migration | migrate a 1.0 value | migrated object; validates |
| T02 | unit (Py) | chain 1→2→3 | migrate a 1.0 value | runs both in order |
| T03 | unit (Py) | migration raises | run | nothing written; `failed` set; reason `settings_invalid` |
| T04 | unit (Py) | last tag's schema with a field `int`, code has it `str`, same major | `rules_schema_versions.py` | fails naming the field |
| T05 | unit (Py) | `rules.schema()` | generate | every section has `version` |
| T06 | integration (Postgres) | pipeline runner start with a 1.0 row | start | value, version and audit row in one transaction (kill mid-way: none) |
| T07 | unit (Go) | stored major older, not migrated | `PATCH` | `409 settings_need_migration` |
| T08 | unit (Go) | version `failed` | `PATCH` without / with `replace` | `409` / `200` and `failed` cleared |
| T09 | unit (Go) | `PATCH` ok | read keys | `rules_version.<s>` written with the value |
| T10 | role | `PATCH`, reset | nil, viewer, play sign-in | `401`, `403`, `403` |
| T11 | unit (Go) | reset | call | both keys deleted; audit `before` has the value |
| T12 | lint | `catalog` job | section owned twice / unknown section | fails |
| T13 | vitest | Rules page | section owned by an off component; `failed` section | "Part of …" note; migrate-failed note and full-form save |
| T14 | rehearsal (CI) | 51.18 scenario with a test-only major bump | update then roll back | component runs on both sides |

## Edge cases

- **A section with no stored row** (never changed) needs no migration; the version key is
  written on the first save.
- **Two pipelines at once** (a switchover, Epic 37): only the pipeline running as the home
  runs the pass, and the write is a compare-and-set on the stored version, so a second run
  finds nothing to do.
- **Environment defaults** (`ENV_DEFAULTS`, the LLM URL) are applied after migration, as today.
- **`server.toml` keys** a component owns (`toml:llm.enabled`) are not versioned here; 51.03
  imports them once into desired state.
- **Schema-only modules** (`search_settings.py`, `updates_settings.py`,
  `system_health_settings.py`, `cluster/machines_settings.py`) declare versions the same way;
  their sections are read by the API, so the API checks `failed` before using them and falls
  back to its compiled defaults with a logged `settings_invalid`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/settings.py` | `SCHEMA_VERSION` lookup, `register_migration`, `migrate()`. |
| `pipeline/src/maktaba_pipeline/rules.py` | `schema()` writes `version`; `read()` reads `rules_version.<s>`. |
| `pipeline/src/maktaba_pipeline/components/settings_migrate.py` (new) | Start-up pass of §3, status reporting through 51.02's store. |
| `pipeline/src/maktaba_pipeline/runtime.py` | Run the pass on the home before stage selection. |
| `pipeline/tests/engine/test_rules_settings.py` | T05. |
| `pipeline/tests/components/test_settings_migrate.py` (new) | T01–T03, T06. |
| `api/internal/handlers/rules/schema.json` | Regenerated with versions. |
| `api/internal/handlers/rules/rules.go` | Version write, `409 settings_need_migration`, `replace`, `component` and `version` in `GET`. |
| `api/internal/handlers/rules/rules_test.go` | T07–T10. |
| `api/internal/handlers/components/settings.go` (new) | `POST /api/components/{id}/settings/reset`. |
| `api/components_cli.go` | `reset-settings`. |
| `web/src/pages/Settings/RulesSection.tsx` | "Part of" note, migrate-failed state, full save. |
| `web/src/pages/Settings/RulesSection.test.tsx` | T13. |
| `web/src/i18n/en.json`, `ar.json` | §6. |
| `tools/ci/rules_schema_versions.py` (new) | T04: compare with the last tag's `schema.json`. |
| `.github/workflows/_lint.yml` | Run it (with tags fetched). |

### Tests

- T01–T03, T05, T06 in the pipeline tests (`uv run --extra dev pytest`, Postgres tier for T06);
  T04 in the lint job; T07–T11 in the Go `unit` job; T12 in `catalog`; T13 in the web unit job.
- **CI only:** T14 in the update rehearsal (51.18).

### Rollout

Ships with Phase 2. Every section starts at 1.0, so the first release changes nothing on disk
until a section's version is bumped.

### Rollback

An older release ignores `rules_version.*` keys and reads sections field by field, as today.

### Dependencies

51.08 (admin routes and audit), 51.01 (`settings` owners, `settings_invalid`), 51.02 (status).
