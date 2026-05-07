# Story 7.15 — Settings & system endpoints

Endpoints from §9.7. Reads everything readable; writes only what is
UI-editable per §11.1 (runtime knobs only).

**AC-1 — Read settings (with redaction).**
- **Given** a config containing `api_key` fields,
- **When** `GET /api/settings` is called,
- **Then** the response is the merged effective config (file + env +
  `app_settings` table) with every secret-bearing key replaced by
  `"<redacted>"` and a sibling `*_present: true`.

**AC-2 — Patch settings (DB-backed only).**
- **Given** a request to PATCH a runtime knob (e.g. `search.fts_weight`),
- **When** the value is in range,
- **Then** the change is persisted to `app_settings` (one row per key),
  takes effect within one settings reload (5 s polling backstop or
  `LISTEN settings_changed` notification, whichever is sooner), and the
  response is the merged effective config.

**AC-3 — Patch denied for non-runtime keys.**
- **Given** a request to PATCH `database.url`,
- **When** sent,
- **Then** the response is `403 Forbidden` `type: setting-not-runtime`.

**AC-4 — STT backends listing.**
- **Given** any deployment,
- **When** `GET /api/settings/stt-backends` is called,
- **Then** the response enumerates `{name, available, version,
  models, hwaccel, cost_per_minute_usd?}` for each backend, sourced from
  Pipeline gRPC `ListBackends` and cached 60 s.

**AC-5 — STT dry-run.**
- **Given** a backend + config,
- **When** `POST /api/settings/stt-test` is called,
- **Then** Pipeline runs a 10 s synthetic-speech transcription and
  returns `{ok, latency_ms, sample_text, error?}`.

**AC-6 — `app_settings` schema.**
- The `app_settings` table is owned by this story. Schema:
  ```
  CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
  );
  ```
  A trigger fires `NOTIFY settings_changed, '<key>'` on INSERT/UPDATE.
  A separate channel `profiles_changed` is used by Streaming for the
  client-profile registry.

**Test cases:**
- Integration: a PATCH that fails validation returns 422 with the
  invalid field listed.
- Integration: the settings change Postgres NOTIFY is received by a
  second API replica within 1 s.
- Security: never returns a value that looks like a secret (regex on
  `key|token|password|secret` in the response body verifies redaction).

**Edge cases:**
- Settings drift between two API replicas during a partial NOTIFY loss —
  the 5 s poll backstop reconciles within at most 5 s. Test case:
  simulate dropped NOTIFY → state converges by next poll.
- A patch to a value that bricks search (e.g. `fts_weight = -1`) is
  rejected by the validator with 422; the config never reaches the
  runtime in a broken state.
