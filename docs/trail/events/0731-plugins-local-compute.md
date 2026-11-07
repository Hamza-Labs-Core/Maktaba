---
schema: 1
kind: pr
pr: 731
title: Plugins ask the home's own models, local compute (Epic 54, story 54.21)
date: 2026-11-06
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
prs: [717]
---

## What happened
- Plugin API 1.1 adds the WIT interface `compute`: `compute.embed` through a new pipeline RPC `EmbedTexts` (the semantic search model, as passages), `compute.llm` through Ollama with the model in a new rules section `plugin_compute` (the tag check's by default). A model server off the home and the LAN is refused, checked on every connection. Questions share the `lookup` pool through `compute_leases` rows the claim counts as busy, and core keeps at least 2 of every 3 slots (`claim_shares` owner `plugin_share:lookup`). Migration 0196.

## What we learned
- Wasmtime's component linker matches `maktaba:plugin/x@1.0.0` imports against the host's `@1.1.0` by semver, so a minor bump keeps old guests working as long as no shared type (such as `call-error`) changes. It also links a `@1.2.0` import, so the import audit (`plugin_api_newer`) is the only thing that refuses a newer minor.
- `cargo build` of the guests does not notice a WIT change (the `generate!` macro does not track the file). Touch the guest sources, or the tests run on old guests. Check the import versions with `strings x.wasm | grep maktaba:plugin`.
- The affected-tests gate counts a test in a new file for a new source only. For old sources (claim.py, server.go) it took the new test files because the coverage map named the package; schema-revision constants still need a `No-Test-Change` trailer.
- The Go `rules.Effective` does not apply the pipeline's `ENV_DEFAULTS` (`MAKTABA_OLLAMA_URL`). Code that reads `rules.tagcheck.url` from Go has to apply the variable itself.
- migration-lint refuses a plain `CREATE INDEX` even on a new empty table: `CONCURRENTLY` in its own statement block of a NO TRANSACTION file.

## Open
- Phase 4's wave 4 (54.31) can use `compute` once this is released.
