---
schema: 1
kind: pr
pr: 1027
title: Staff sign-in for the operator console, unstalled and merged
date: 2026-11-09
session: bf61b654-4a8e-4f3e-af2b-8a0228bf3fba
---

## What happened
- #1027 (story 50.33 part 1) had stalled since 2026-11-08 13:48Z. It conflicted with main and failed CodeQL, affected-tests, unit, cloud-rehearsal and report-symbolicated. Main merged in cleanly apart from the CHANGELOGs (`tools/dev/merge-main.sh`); the cloud migration slots 00500011–13 were still free.
- CodeQL (go/reflected-xss, high): StaffGate's hand-written status recorder overrode `Write`, so every handler's bytes reached a `ResponseWriter.Write` in our code. chi's `WrapResponseWriter` replaced it, which also keeps the handler's `Flusher`.
- report-symbolicated: the api refused to start its admin listener without the two staff secrets, and the scenario signed in as a customer `service_admin`, which part 1 removes. The rehearsal compose got rehearsal-only staff secrets; the scenario runs `maktaba-cloud staff add` in the api container, enrols with a password and TOTP, and signs in with both.
- affected-tests: the erasure and report-triage audit rows became `actor_kind = staff` with no test checking it; tests now do. A later run flagged four web/cloud tests that name `GET /v1/account/me` as stale. That was a false positive (the customer route stays; only the admin Worker stopped passing it), cleared with a `No-Test-Change` trailer.
- unit: a streaming test with fixed sleeps (`TestTheReplacedKeyGoesOnceItsURLsArePast`) ran 1.2 s against a 400 ms soft cap; it now waits for the drop. The cloud-web edge test compared the Worker's `CONSOLE_SIGN_IN` with the listener's removed `ConsoleSignIn`; the Worker's stopgap went too.
- load-small: relay memory per tunnel was 20.6% worse than main. The staff handler computed an argon2id timing hash (64 MiB) at package init, in every role of the binary. It is now made at the first sign-in (`sync.OnceValue`).

## What we learned
- A package-level value that does real work runs in every role of a multi-role binary. Keep expensive ones lazy; load-small's per-tunnel memory comparison catches what unit tests can't.
- A stopgap carried through several layers (here the listener and the admin Worker) must be removed from all of them in the same PR; the edge test that compared the two lists caught the missed one.
- A rehearsal scenario that signs in to the console must follow the console's sign-in: it drives the CLI in the container and the real enrol and sign-in steps.

## Keep doing
- Read each failing job's log before touching code: four of the six failures had different causes.

## Open
- The customer auth handler (`cloud/internal/handlers/auth`) still computes its argon2id timing hash at init, so every role still holds that 64 MiB.
- Part 2 (passkeys) is #1067.
