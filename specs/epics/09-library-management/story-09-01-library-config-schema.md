# Story 9.1 — Library config schema and validation

The `libraries.settings` JSONB blob is the per-library config profile
(§5). This story locks the schema, the merge semantics, and the
boot-time validation.

**AC-1 — Schema enforcement.**
- **Given** a `settings` blob,
- **When** validated,
- **Then** the following keys are recognized: `language`
  (`auto`|ISO-639-1), `multi_audio` (bool, default false), `stt`
  (`{backend, model, profile, initial_prompt?, max_usd_per_month?}`),
  `embedding` (`{model, device}`), `diarize` (bool), `chapter_inference`
  (bool), `auto_tag_topics` (bool), `default_subtitle_lang` (ISO-639-1),
  `ignore_globs` (string[]), `sweep_interval_sec` (integer, 0 disables).
  Unknown keys are stored but a warning is emitted to the API response
  on PATCH.

**AC-2 — Defaults inheritance.**
- **Given** a library with `stt: {backend: "whisper-mlx"}` only,
- **When** a worker reads the effective config,
- **Then** missing keys are inherited from `[stt.default]` in
  `pipeline.toml` (§11.4), recursively. The library config can override
  any layer below it.

**AC-3 — Settings change triggers re-evaluation.**
- **Given** an update that bumps the STT model,
- **When** PATCH succeeds,
- **Then** a `library.settings_changed` NOTIFY fires; the orchestrator
  marks newly-arriving videos with the new model from this point. Existing
  videos are *not* re-processed automatically — the user must trigger
  reprocess (Epic 7 Story 7.5).

**Test cases:**
- Unit: every recognized key has a positive + negative validation test.
- Unit: deep-merge with `[stt.default]` fixture produces expected
  effective config.
- Integration: a malformed `stt.backend = "invalid"` returns 422 from
  the PATCH handler with the offending path.

**Edge cases:**
- A library written with a future schema version (forward compat) — keys
  are preserved on read; unknown keys round-trip unchanged.
- A `language` change does not retroactively re-tag old videos. Document
  this in the API reference (and add an action button in the UI for
  bulk re-tag — out of scope here).
