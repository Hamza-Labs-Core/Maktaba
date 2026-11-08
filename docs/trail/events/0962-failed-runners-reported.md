---
schema: 1
kind: decision
pr: 962
title: A home reports a failed plugin runner, to admins only, with the stages that run in the worker
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [833]
---

## What happened
- Open since #833: whether a home should report runners stuck in `failed` while their stages are `plugin`. The decision is yes, to admins only. A failed runner leaves its stages running in the worker, outside their sandbox. No job is lost and no viewer feature stops.
- The supervisor names each component's failed runners (`detail.failed_runners`; held or idle runners and non-runner processes are left out).
  - System health's new Plugin runners line maps each failed runner to the stages `job_stages` sets to `plugin` in its group, on every machine.
  - `/status.json` gains an admin-only `plugin_runners` group: `down` when a runner failed, and it never touches `overall`, `features_down` or the banner.
  - An admin's strip shows a line for it, on the web, Apple and Android, only while the group is `down`.
  - The Plugins and Updates pages list the failed runners and link to Components.

## What we learned
- A failed process gets three slow retries (`FailedRetries`), then stays down until it is started again. Help text that promises a restart must say so.
- `/status.json` is public and drives every app's viewer banner. A group no viewer feature waits on needs an admin-only flag, or one failed runner would turn `overall` degraded for viewers.
- The affected-tests gate maps a source to the tests main's coverage links to it, not to the test you wrote: Android's `HomeStatus.kt` (client module) wanted `ApiClientTest`, not core's `StatusStripTest`; each page that renders a new component wanted its own page test.
