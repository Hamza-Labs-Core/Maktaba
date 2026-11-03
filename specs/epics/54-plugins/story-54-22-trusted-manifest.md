# Story 54.22 — First-party plugins: the trusted manifest and release signing

> Epic 54 · Phase 4 · Depends on: 54.01, Epic 51 (51.15) · Needed by: 54.23, 54.24, 54.27

## Why

The owner wants Maktaba's own stages to be plugins on the same interface as third-party ones
(README §15). They need rights no third-party plugin may have: their own SQL, library files,
ffmpeg, the GPU, sometimes the network. Those rights must be **declared**, **reviewed**, and
**unlockable only by Maktaba's release signature**, or "first-party" becomes a back door any
package could claim.

## Description

### 1. The manifest schema grows, once

`shared/plugins/manifest.schema.json` (54.01) gains:

- `runtime`: `"wasm"` (default) or `"native-python"`.
- `[trusted]`: `runner`, `db.read`, `db.write`, `statements`, `settings`, `files.read`,
  `files.write`, `exec`, `gpu`, `net`, `service`, `component` (README §15.4, §15.5).
- In `[[stage]]`, the `StageDef` fields third-party plugins can't use: exact `name`, `pool`,
  `lane`, `lane_order`, `edges` (to stage names), `entry_for`, `debounce_sec`,
  `debounce_max_sec`, `external_write`, `hidden`, `placement`, `model_rules`, `relay`,
  `after_topup`, `every_minutes_rule`, `unreleased`, `runner`, `fingerprint`.
- `[resources]`: `ram_mb`, `gpu`, `models` (Epic 51 requirement expressions).

Each of these is marked `"x-trusted-only": true` in the schema. The validator (Go, Rust,
Python and the store, all from the one file) answers `plugin_trusted_field` for any of them in
a package that is not trusted (§2).

Reserved for trusted packages: the id prefix `tv.maktaba.` and every stage name that exists in
`shared/plugins/graph.golden.json`.

### 2. What makes a package trusted

A package is trusted when **all** hold:

1. its `files.json` digest is listed in the running release's `checksums.txt`, whose Sigstore
   signature was verified against the release identity (Epic 51 H6, `shared/go/releaseverify`);
2. its `version` equals the running server's version;
3. its id starts with `tv.maktaba.`.

The check runs in the Go API at load (`api/internal/plugins/trusted.go`) and again in the
runner before it imports the package (54.23), each from its own copy of the verified list. A
failed check refuses the package with `plugin_trusted_unsigned` (or `plugin_trusted_version`),
the component goes *Failed* with that reason, and nothing of it runs.

There is no setting, flag or environment variable that marks a package trusted. Developer
builds use the existing `-tags e2etrust` build, which trusts a developer index (Epic 51 §4.9).

### 3. The store refuses trusted packages

54.16 gains check C3a: a submission with `runtime = "native-python"`, a `[trusted]` table, a
trusted-only field, a `tv.maktaba.` id or a reserved stage name is blocked with
`store_trusted_forbidden`. The catalog never lists a `tv.maktaba.*` id.

### 4. What the admin sees

The Plugins page (54.13) lists first-party plugins under **Built into Maktaba**, with a
"What it can do" sheet generated from `[trusted]` and the public capabilities, in Maktaba's
words (`fp.trusted.<capability>` in English and Arabic). There is no grants dialog for them:
turning the component on is the decision (Epic 51).

## Acceptance criteria

- **Given** a package with `[trusted]` whose digest is in the release's verified checksums and
  whose version is the server's,
  **when** the API loads it,
  **then** it is accepted as trusted.
- **Given** the same package with one byte changed,
  **when** it is loaded,
  **then** `plugin_trusted_unsigned`, the component is *Failed*, and no stage of it is registered.
- **Given** a release-signed package from the previous release,
  **when** the server of the new release loads it,
  **then** `plugin_trusted_version`.
- **Given** a third-party package that declares `db.write` or names a stage `transcribe`,
  **when** it is installed from a file with unreviewed plugins allowed,
  **then** `plugin_trusted_field` and nothing is installed.
- **Given** a store submission with a `tv.maktaba.` id,
  **when** checks run,
  **then** C3a blocks it.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit ×4 (go, rust, py, store) | schema corpus: every trusted-only field in a third-party manifest | validate | `plugin_trusted_field` each |
| T02 | unit (go) | fake release with signed checksums | load trusted package / tampered / old version / wrong prefix | accept / unsigned / version / unsigned |
| T03 | unit (py) | runner's own check | same corpus as T02 | same answers |
| T04 | integration (store) | submission with `[trusted]` | checks | C3a blocks |
| T05 | unit | `fp.trusted.*` strings | words | en + ar for every trusted capability |
| T06 | CI (`plugins-trusted`) | all of the above | run | green |

## Edge cases

- **A dev build without `e2etrust`** (a release with a broken signature never installs, Epic
  35): its first-party packages are not trusted, so no runner starts. Until wave 9 the stages
  still run in-process (`impl = inproc`, 54.25; their definitions come from the manifests
  embedded in the build) and the Components page says why; after wave 9 developers use
  `e2etrust`, as for packs today.
- **Two packages with the same `tv.maktaba.` id**: the one in the verified list wins; the other
  is refused.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/manifest.schema.json` | §1 fields, `x-trusted-only`. |
| `api/internal/plugins/manifest/`, `plugind/src/manifest.rs`, `plugins/sdk/python/…/manifest.py`, `store/manifest/` (TypeScript, the same JSON schema) | Validators honour `x-trusted-only`. |
| `api/internal/plugins/trusted.go` (new) | §2 check against `shared/go/releaseverify`. |
| `store/checks/c3a_trusted.ts` (new) | §3. |
| `web/src/pages/Plugins*`, `web/src/i18n/{en,ar}.json` | §4 "Built into Maktaba" and the sheet. |
| `.github/workflows/` `plugins-trusted` job | T06. |

### Tests

- Unit T01–T03, T05; integration T04; CI T06.

### Rollout

Dark: no first-party package exists until 54.27 builds them.

### Rollback

Release rollback; nothing in the database depends on it.

### Dependencies

54.01 (schema), Epic 51 51.15 (signed release index and checksums).
