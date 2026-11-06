---
schema: 1
kind: pr
pr: 666
title: A whole-server components-live scenario for a process component
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 641
---

## What happened
- components-live now runs `maktaba-server serve` of the whole machine on a scratch database, with the llm component's process a stand-in (`tests/components/fakeollama/ollama`), and turns llm off and on through the Components page's calls: process table, supervisor status row and audit rows asserted; the server's log kept.
- Locally: stopped 0.014 s after the PUT, a new process 0.076 s after enable, running (supervisor probe) 10.2 s later. The first commit's message said 3.3 s; that figure was not measured.

## What we learned
- A whole server needs little to run in a test: `MAKTABA_CONFIG` with every address set to a free loopback port, `[postgres] enabled = "off"` with `[database] url`, `MAKTABA_PYTHON` for the pipeline, `--bin-dir` for the API and streaming, and the working directory at the repo root so the API's auto-migrate finds `shared/db/migrations`.
- On a machine running a live Maktaba, the STT workers' log ports (9131 and up) clash: `[pipeline] instances = 0` keeps them out.
- An Ollama model asset counts as installed only when its manifest is on disk with the pinned digest, so with a stand-in llm reads `waiting` (a model never closes the gate); the scenario asserts "not off at the new generation".
- Measure a stop from before the PUT, not after it returns: the supervisor had already stopped the process by the time the response came back.

## Stop doing
- Writing a measured figure into a commit message before reading it from a run.
