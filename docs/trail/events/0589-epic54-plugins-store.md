---
schema: 1
kind: decision
pr: 589
title: Epic 54 spec: offline plugins and a Maktaba store
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 43b18152f. Third-party plugins extend the queue with no internet access. Plugins are WebAssembly components run by a sandboxed Rust host (`maktaba-plugind`, Wasmtime). Sockets and files are never given to plugins, the host runs in each OS's sandbox, and a CI-only job proves zero network connections on macOS, Linux and Windows.
- The pipeline keeps the queue and the API checks every call against admin-granted permissions; each plugin is an Epic 51 component.
- The store (maktaba.tv; docs on maktaba.dev) has automated checks plus human review, developer and Maktaba signatures under an offline root, a signed revocation list, offline file install behind an "Allow unreviewed plugins" opt-in, and paid plugins (the first draft proposed Stripe Connect at 85/15; Stripe was ruled out in #591).

## Open
- Open owner questions in README section 14 (answered in #591).
