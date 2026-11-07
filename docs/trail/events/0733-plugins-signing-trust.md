---
schema: 1
kind: pr
pr: 733
title: Plugins Phase 2 part 1, signing and the chain of trust (Epic 54, 54.14)
date: 2026-11-06
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [683, 693, 707, 717, 723]
---

## What happened
- First PR of Epic 54 Phase 2: the plugin chain of trust in Go (`shared/plugins/pluginsig`, the home), Rust (`plugind/crates/sig`, the SDK) and TypeScript (`store/signing`, the store's signer), agreeing on one set of vectors the TypeScript signer writes (`shared/plugins/testdata/signatures/`). The supervisor verifies a package's two signatures at install and the API records the review level; `mkp keys` and `mkp pack --key` sign; a new `store` CI job runs the online key rotation drill. Migration 0196. Ships dark: the committed root has no keys.

## What we learned
- Go's `encoding/json` escapes `<`, `>`, `&` and U+2028/U+2029 and silently keeps the last of a duplicate key; JavaScript's `JSON.parse` keeps lone surrogates and also takes the last key; Rust's serde_json refuses lone surrogates. Canonical JSON for signatures needs its own small parser in each language (duplicate keys, integers only, no surrogate escapes, UTF-8 checked first), not the standard library's.
- `files.json` must be hashed in canonical form, not as written: the Go writer (`json.Marshal`) and `mkp` (serde_json) wrote different bytes for a path with `<`.
- Unpacking first and verifying after hides the digest step: `UnpackPlugin` now also answers the head it read on a digest mismatch, so a signed package's tampered file is reported as `plugin_signature_invalid: digest`.
- The home's policy check had to move after the supervisor's upload: only the supervisor knows the level the signatures prove, so a policy refusal now discards the placed folder too.
- The SQLite migration runner rewrites `ADD COLUMN IF NOT EXISTS` only when it is the one statement of its `StatementBegin` block.
- Local checks: `tools/ci/tests` needs PyYAML (the pipeline's venv has it); `tools/affected-tests` maps shared JSON tables to no test, so a gate or rating change needs a direct test beside the table row.

## Open
- PRs 2-5 of the series: the store service (54.15), checks and review (54.16), the home's catalog loop and kill switch (54.17, with Postgres's copy of the trust state), the store browser (54.18).
- Embedding the current revocation list and store keys at release build time (release workflows, left alone while the release-signing fix is in progress).
