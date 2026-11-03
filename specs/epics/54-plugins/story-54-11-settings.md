# Story 54.11 — Settings pages declared as data

> Epic 54 · Phase 1 · Depends on: 54.07, Epic 51 (51.13) · Needed by: 54.13

## Why

Most plugins need a few settings (a threshold, a language, an interval). Letting a plugin ship
its own web page would mean running its code in the admin's browser, next to the admin's
session. Maktaba already draws every `rules.*` section from a JSON schema
(`web/src/pages/Settings/RulesSection.tsx`). Plugins get the same: they describe the form, and
Maktaba draws, validates and stores it.

## Description

### 1. The schema

`ui/settings.schema.json`, a JSON Schema subset:

- root `type: object`, ≤ 40 properties, no nesting deeper than one object level;
- property types: `string` (with `enum`, `maxLength` ≤ 2,000, `pattern` from a safe subset),
  `integer`, `number` (with `minimum`/`maximum`), `boolean`, `array` of enum strings;
- `x-maktaba-title`, `x-maktaba-help`: keys into the plugin's `i18n` files (en and ar required);
- `x-maktaba-widget`: `text`, `textarea`, `select`, `slider`, `switch`, `library-picker`
  (values are library ids the plugin is granted), `language-picker`;
- `default` required for every property;
- **forbidden:** `format: password`, any `secret`, `x-*` other than the above, `$ref` outside
  the file, URLs in strings, `readOnly` tricks. No field may be named `token`, `password`,
  `api_key`, `secret` (case-insensitive): a plugin with no internet has nothing to sign in to
  (README H8), and a secret field would only be a hiding place.

### 2. Storage and versions

- Settings live in `app_settings` under `plugins.<id>` (the same table as `rules.*`, but not
  under the `rules.` prefix, so the API's `rules` loader and `GET /api/rules` never see them).
  Epic 51.13 versions a `rules.<section>` with its own key, `rules_version.<section>` =
  `{"major":…,"minor":…}`, and Python `SCHEMA_VERSION` / `register_migration`
  (`pipeline/src/maktaba_pipeline/settings.py`). A plugin has no Python class there, so it
  follows the same pattern with its own key: `plugins_version.<id>` = `{"version":
  "<plugin version>", "data_version": N}`, written with the settings in one transaction.
- The catalog entry of the plugin lists `settings = ["plugins.<id>"]` (54.37 §1). Epic 51's
  reset, `POST /api/components/plugin.<id>/settings/reset` (`Service.ResetSettings`, audit
  `settings.reset`), today resets only `rules.*` sections; this story teaches it the
  `plugins.` key, so the Plugins page's **Reset to defaults** and the Components page call the
  same thing.
- `PUT /api/plugins/{id}/settings` validates against the installed version's schema (Go, with
  the same validator the web uses for `rules.*`), stores, audits, and bumps the settings hash
  that plugin stage fingerprints include (54.06 §4), so a change reruns what depends on it.
- On update, new fields take their defaults; removed fields are dropped; a changed type falls
  back to the default and the Plugins page says which field was reset.
- The plugin reads settings with `job.settings()`; values are passed in `Run`, never fetched.

### 3. Drawing

The web's rules form renders the schema with the plugin's strings in the admin's language and
RTL for Arabic. Validation messages are Maktaba's own (en + ar). A **Reset to defaults** button
is always present.

## Acceptance criteria

- **Given** a schema with a field named `api_key`,
  **when** the package is validated,
  **then** it is refused with `settings_secret_forbidden`.
- **Given** a valid schema,
  **when** the admin opens the plugin's settings in Arabic,
  **then** labels and help come from the plugin's `ar.json`, the form is RTL, and messages are
  Maktaba's Arabic words.
- **Given** an out-of-range integer,
  **when** saved through the API directly,
  **then** `400 settings_invalid` (server-side, not only in the form).
- **Given** the setting `threshold` changed,
  **when** the reconciler passes,
  **then** the plugin's stages rerun on items whose fingerprints include the old hash.
- **Given** a viewer,
  **when** it calls `GET` or `PUT /api/plugins/{id}/settings`,
  **then** 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit ×2 (go, ts) | schema corpus valid/invalid | validate | per code |
| T02 | unit (web) | render each widget, en and ar | render | labels, RTL, defaults |
| T03 | integration (PG, go) | bad values | PUT | 400 |
| T04 | integration (PG, py) | change a value | reconcile | rerun on affected items |
| T05 | role test (go) | GET/PUT settings | no one / viewer / cloud-linked / admin | 401 / 403 / 403 / 200 |
| T06 | integration (PG, go) | update with changed type | apply | default + notice |

## Edge cases

- **Library picker after a library is deleted**: the id is dropped from the value at the same
  time as from grants (54.08).
- **Settings for a held or incompatible plugin**: editable; used when it runs.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/settings.schema.json` (new) | The meta-schema of the allowed subset. |
| `api/internal/plugins/settings.go` (new) | Validate, store, hash, audit. |
| `api/internal/components/settings.go` (Epic 51) | `ResetSettings` and purge reach the `plugins.<id>` and `plugins_version.<id>` keys. |
| `web/src/pages/Settings/RulesSection.tsx` | Accept a plugin schema + string source; new widgets. |
| `web/src/pages/Admin/Plugins/Settings.tsx` (new) | Wrapper page. |
| `web/src/i18n/{en,ar}.json` | Messages. |

### Tests

- Unit T01, T02; integration T03, T04, T06; role T05.

### Rollout / Rollback

Ships with Phase 1; additive.

### Dependencies

54.07, 54.37 (the catalog entry's `settings`); Epic 51 51.13 (the versioning pattern and the reset route).
