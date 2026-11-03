# Story 54.14 — Signing, the trust root and the revocation list

> Epic 54 · Phase 2 · Depends on: 54.01 · Needed by: 54.15, 54.17

## Why

A home must be able to tell, offline, three things about a plugin file: who made it, that
Maktaba reviewed this exact file, and that Maktaba has not since withdrawn it. Epic 51's packs
are signed by the release workflow's Sigstore identity, which means "built by Maktaba from
Maktaba's commit". A third-party plugin is neither, so plugins need their own, small chain of
trust (README H9), and a revocation list that cannot be frozen or rolled back.

## Description

### 1. Keys

| Key | Held by | Signs | Lifetime |
|---|---|---|---|
| **Plugin root keys** R1, R2 (Ed25519) | the owner, offline (two hardware keys in two places) | `store-keys.json` | until rotated by a server release |
| **Store online keys** S (Ed25519) | the store service (54.15), as a Worker secret of the store's own Cloudflare Workers, with an offline copy held by the owner; never in the cloud | packages (`store.sig`), `catalog-*.json`, `revoked.json`, `revoked-head.json` | 1 year, listed in `store-keys.json` with `expires` (13 months) |
| **Store entitlement keys** E (Ed25519, Phase 3) | the store service, a separate Worker secret | plugin entitlements (54.19 §6) | 1 year, listed in `store-keys.json` with `use: "entitlement"` |
| **Developer keys** D (Ed25519) | each developer, on their machine (54.12) | packages (`developer.sig`) | until the developer revokes it |

- `plugins-root.json` (R1 and R2 public keys, threshold 1) is **embedded** in every server
  binary (Go `embed`) and in the SDK. Rotating a root key ships in a server release and the old
  key signs the new root (TUF's root chaining, nothing more).
- `store-keys.json` `{version, expires, keys: [{id, public, use ("store" | "entitlement"),
  not_before, not_after}]}` is signed by a root key, fetched with the catalog, and pinned by
  `version` (anti-rollback). A key is valid only for its `use`: an entitlement key cannot sign a
  package and a store key cannot sign an entitlement.

### 2. Package signatures

Over the canonical bytes of `files.json` (sorted keys, no spaces), each signature file holds
`{key_id, sig, signed: {...}}`:

- `developer.sig`: `signed = {plugin_id, version, files_sha256}`, by D.
- `store.sig`: `signed = {plugin_id, version, files_sha256, developer_key_id, review_level
  ("automated" | "reviewed"), reviewed_at, capabilities_sha256}`, by S. It covers the developer
  key id, so a package cannot be re-signed by another developer and keep the store's mark.

**Review levels** a home records: `none` (no signature), `developer` (D only, valid),
`automated` (S, automated checks only: an update that added nothing), `reviewed` (S, a human
reviewed it).

### 3. Verification at the home

`shared/go/pluginsig` verifies, in order: `plugins-root.json` (embedded) → `store-keys.json`
(signature by a root key, not expired, version ≥ the newest seen) → `store.sig` (key in
`store-keys.json`, valid at `reviewed_at`) → `developer.sig` (the key id named in `store.sig`)
→ every file's sha256 and size against `files.json`. Any failure → `plugin_signature_invalid`
with the step. The newest-seen versions are kept in `<data>/state/plugins-trust.json` and in
Postgres.

### 4. The revocation list

`revoked.json` `{version, issued, expires (issued + 48 h), entries: [{plugin_id,
versions (semver range), developer_key_id?, action ("disable" | "warn" | "delist"), reason_code,
issued}]}`, signed by S.

`revoked-head.json` `{version, issued, sha256 (of the current revoked.json)}`, also signed by S,
is the small file homes poll every 5 minutes (54.17); it only tells a home that a newer list
exists, and the list itself is still verified on its own.

- A home refuses a list with a lower `version` than the newest it has seen, or one past
  `expires`.
- An **expired** list does not stop plugins (README H10, T12): the Plugins page warns
  "Couldn't check for withdrawn plugins since {date}", and installs and updates from the store
  wait until a fresh list arrives. Installs from a store-signed file proceed with the warning.
- Each server release embeds the list as of its build (`revoked-embedded.json`), used when it
  is newer than the stored one.
- Applying entries is 54.17's.

### 5. Key compromise drills

- **Store online key:** the owner signs a new `store-keys.json` without it (root key), the
  store re-signs every live package with the new key in one batch job, homes pick both up on
  the next fetch. A drill runs in CI (`store` job) with test keys.
- **Developer key:** the store adds a `developer_key_id` entry to the revocation list with
  action `warn` or `disable` per incident, and blocks new uploads signed by it.
- **Root key:** the other root key signs a new root that drops it; a server release carries it.

## Acceptance criteria

- **Given** a package signed by a developer and countersigned by the store,
  **when** a home verifies it offline,
  **then** it passes and records review level `reviewed` or `automated` from `store.sig`.
- **Given** the same package with one byte of `plugin.wasm` changed,
  **when** verified,
  **then** `plugin_signature_invalid: digest`.
- **Given** `store.sig` copied onto a package signed by a different developer key,
  **when** verified,
  **then** it fails (`developer_key_mismatch`).
- **Given** a home that has seen revocation list version 120,
  **when** it is served version 119 (valid signature),
  **then** it refuses it and keeps 120.
- **Given** the revocation list expired 3 days ago (no network),
  **when** the Plugins page opens,
  **then** plugins keep running, the warning shows, and store installs wait.
- **Given** a `store-keys.json` signed by a key not in the embedded root,
  **when** fetched,
  **then** it is refused.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go) | test root, store, developer keys | sign + verify | each review level |
| T02 | unit (go) | tamper each file, each sig, swap sigs | verify | fail at the right step |
| T03 | unit (go) | store-keys expired / old version / bad root | verify | refused |
| T04 | unit (go) | revocation list older / expired / newer embedded | load | rules of §4 |
| T05 | unit ×3 (go, rust, ts) | shared vectors in `shared/plugins/testdata/signatures/` | sign (ts) and verify (all) | same answers |
| T08 | unit (go) | an entitlement signed by a `store` key; a package signed by an `entitlement` key | verify | both refused (`key_use_mismatch`) |
| T06 | CI (`store`) | online key rotation drill | rotate | homes accept new, refuse removed |
| T07 | unit (go) | canonical JSON | sign/verify across Go and Rust | stable bytes |

## Edge cases

- **A plugin signed before a store key's `not_after`, verified after**: valid; keys sign at a
  time, and `reviewed_at` must be within the key's window.
- **Clock wrong on the home** (no NTP): expiry uses the newest of the system clock and the
  newest `issued` seen, so a clock in the past cannot make an old list look fresh.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/go/pluginsig/` (new) | Verify chain, canonical JSON, revocation list loader. |
| `plugind/crates/sig/` (new) | Rust verifier (SDK and harness). |
| `shared/plugins/plugins-root.json` (new) | Root public keys (placeholders until the owner makes the keys; CI uses test keys with `-tags e2etrust` as Epic 51 does). |
| `shared/plugins/testdata/signatures/` (new) | Vectors. |
| `store/signing/` (new, 54.15) | Signer side (TypeScript, WebCrypto Ed25519), checked against the same vectors. |
| `docs/plugins/trust.md` (new) | How the chain works, for developers. |

### Tests

- Unit T01–T05, T07, T08; CI T06 in `store`.

### Rollout

Code ships dark. **The owner makes the two root keys offline** (instructions in
`docs/plugins/trust.md`); until then builds carry test keys only and the store does not
publish. No key material is ever committed or printed.

### Rollback

Release rollback; Phase 1 installs (unreviewed) are unaffected.

### Dependencies

54.01 (`files.json` format).
