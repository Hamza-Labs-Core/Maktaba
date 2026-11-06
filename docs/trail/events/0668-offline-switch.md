---
schema: 1
kind: pr
pr: 668
title: One switch that stops every outbound call (serve --offline)
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 640
---

## What happened
- `maktaba-server serve --offline` / `MAKTABA_OFFLINE=1` sets every existing outbound switch at once (updates, packs, Ollama pulls, telemetry, relay agent, Hugging Face) for the server and the roles that inherit its environment; the API honours the variable on its own too (no licence refresh).
- `ports.Outbound` lists every outbound call with its switch; a test walks the servers' Go code for internet hosts and fails on one it does not list.

## What we learned
- Every call already had its own switch; what was missing was one place that sets them all and a list that keeps new ones honest. Setting the existing environment switches at startup needed no change in any reader.
- The first run of the host test found `api.maktaba.tv` (the licence server and cloud API default), which no switch list had named.
- The supervisor's roles inherit `os.Environ()`, so a variable `serve` sets for itself reaches every child.

## Open
- The pipeline's Python code is not scanned by the host test.
