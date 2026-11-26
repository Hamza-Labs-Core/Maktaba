---
schema: 1
kind: pr
pr: 1788
title: Development dependency alerts and TV login redirect validation
date: 2026-11-26
session: 01a219d4-c627-7cf0-81b5-900ef7ff0a99
agent: codex
---

## What happened
- [PR #1788](https://github.com/Hamza-Labs-Core/Maktaba/pull/1788) merged to main as `bd3b4d6f2e28ab9c05574cfa0013c2f0b50be2aa`, clearing all 69 open Dependabot dev/test advisory ranges across 10 manifests.
- Updated `undici` to 7.30.1, `sharp` to 0.35.5, `decode-uri-component` to 0.5.1, `pytest` to 9.0.3, `basic-ftp` to 6.2.6, `uuid` to 14.0.3, and `minimatch` to 11.0.9 via `typescript-eslint` 8.74.0. Updated `wrangler` to 4.154.0 and synced the pin in `devportal-deploy.yml`.
- Hardened the TV login redirect in `web/src/pages/Login.tsx` to address CodeQL #229 (`js/client-side-unvalidated-url-redirection`). The handler parses `dest` with `new URL(dest, window.location.origin)` and verifies the origin and canonical `/tv/` pathname prefix before navigating.
- Secret-scanning alerts #9–#11 covered synthetic base64 unit-test fixtures and were resolved as `used_in_tests`. CodeQL alerts #186–#187 were dismissed as false positives for the `update-rehearsal.yml` workflow, whose `workflow_run` is gated on pushes to `main`.

## What we learned
- Roku's CommonJS `source-map-resolve` wrapper needs the default export from the updated ESM decoder. The install patch adapts the wrapper.
- Upgrading to `@lhci/cli` 0.16.1 with Lighthouse 13 removes `extract-zip` and `tmp` from the dependency tree. Update the assertion in `lighthouserc.json` to `image-delivery-insight` with the upgrade.
