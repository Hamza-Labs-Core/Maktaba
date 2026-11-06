---
schema: 1
kind: pr
pr: 664
title: Epic 53 story 53.01 — one log line and one redaction list for every part
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- First PR of the Epic 53 series (planned order in the PR body: 53.01, then 53.02+03, 53.04+05, 53.06+07, 53.08+09, 53.10–13, 53.14+15, 53.16/17 home parts). Cloud parts that need Epic 50 (forwarding to the witness, the 14-day cloud store, the Epic 50.10 feed) are left out.
- One schema, one redaction list (`shared/redact/rules.json`) and one table of cases, passed by Go, Python, TypeScript, Swift, Kotlin and Rust. `user_id` becomes `user_h` under a per-machine `log.key`, which the supervisor creates beside `secrets.key`.

## What we learned
- The first Go version scrubbed a value in 18 µs, over the budget. Literal `needs` per rule (e.g. `token=`, `@`, `://`) cut a plain value to ~0.2 µs. The query-secret regex alone costs ~4 µs when it does run.
- Portable regex across RE2, Python, JS, ICU, Java and Rust: no look-around, no back-references, no inline `(?i)` (JS has none), groups as `$1` (Python needs `\g<1>`). Boundary checks (a digit before an IP, a hex char before an IPv6) are done in code from the characters around the match.
- The web's prettier rewrites `redactRules.json`, so the original `rules.json` is kept in prettier's format; otherwise the copy check and the prettier check fight.
- A Swift multi-line raw string drops the newline before its closing `"""#`, and a Kotlin raw string keeps the one after its opening `"""`. The generator accounts for both, so each copy equals the file byte for byte.
- Pipeline unit tests have a 0.1 s soft cap per test: running the lint over all of `src/` in a unit test fails it, so the lint runs from `make lint-py` instead.
- Supervisor tests that call `ChildEnv` without their own `MAKTABA_CONFIG` would write `log.key` into the real keys directory. `TestMain` swaps `ensureLogKey` so they never do.

## Open
- The cloud drops account ids from its lines until a deploy has `MAKTABA_LOG_KEY` (`deploy/cloud-relay/setup.sh` now makes one).
