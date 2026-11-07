---
schema: 1
kind: pr
pr: 762
title: Events between plugins and run-time lineage (Epic 54, 54.40)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [759]
---

## What happened
- Plugin API 1.1 adds `events.emit`; the API checks the declared event, the subject, the payload against the publisher's schema and the subscribers' `events.subscribe` grants, and every plugin job carries a lineage that refuses a 17th hop, a re-entry, and a loop through data (`plugin_write_lineage`, migration 0197). The new cause `plugin_loop` has its label and help in both languages.

## What we learned
- Wasmtime's component linker resolves semver-compatible import names (a guest's `job@1.0.0` finds the host's `job@1.1.0`), so a WIT minor bump keeps old guests linking; the audit's version check was the only thing to move.
- Adding a variant case to the WIT `call-error` would change every guest's import types, so a loop answers `scope` to the guest and the host keeps `plugin_loop` as the job's refusal code; a guest that ignores the refusal still fails.
- `propagate.finish` checks every request against `may_request`: a cross-plugin event needs an allowance of its own (plugin stage to another plugin's stage, marked `plugin-event:`), never a `may_request` entry, or the leaf assertion would refuse the graph.
- A unit-tier pytest cannot start an event loop (its self-pipe is a socket the net guard refuses); a coroutine whose fakes never suspend can be stepped with `send(None)`.
- Slot 0196 was taken by #733 while this was built; the migration moved to 0197 and SchemaRev to 197 before the push.

## Open
- The chart (54.41) and joined machines (54.42) follow.
