---
schema: 1
kind: pr
pr: 635
title: Epic 51 follow-ups: drawer, off/held health, first boot, Arabic, offline install
date: 2026-11-04
session: unknown
source: claude-trail.md
prs: [608]
---

## What happened
- Merge 2fa5cacfa, follow-ups from the live run at 9bf413503. Three agent branches merged into `claude/epic51-followups`: followup-web (c508afb81), followup-packs (7572594d3) and followup-phases (9efc49744).
- An open component drawer re-reads its detail when an install finishes.
- First boot on a fresh home: the packs runner waits for the desired-state seed, retries a busy install every 2 s, and an absent asset never reads failed. Before this, `model.refine` read failed or busy, and Health showed Degraded for about 16 minutes.
- Offline mode refuses an install: `POST /api/components/{id}/install` returns 409 `packs_disabled` before it writes anything, and the CLI refuses the same way. The supervisor answers whether packs are on through a token-protected loopback route.
- Health counts off and held on purpose as healthy: a held process reads `process_held`, and the components line leaves out a switchable part that is off or held.
- Arabic for the health summary, the tagcheck section, process instance names (web, Apple, Android), the 59 stage titles (tested against `graph.py`) and offline import errors, which are now stored as a code. Arabic stage-title tests for the library board and LibraryAdmin (9d9a5a6e4).
- The bundled model server reads `dependency_llm_off` while the language model is off; its jobs stay parked. An outside model server that is down still reads down (706148279).
- Each of a role's processes is its own health line, with id `role-instance` (fd7889bda).
- The test-budget gate was met at its cause, with no cap raised and no test split (348e466ee): the components handler fixtures are built once per test binary and copied for each test (TestMachineRoutesAdminOnly 0.63 s to about 0.2 s); TestResults_StablePaging writes one passage per video in a single transaction, with the two indexes it reads (0.40 s to 0.12 s).
- TestCrashBackoffAndCrashLoop (#645) was also fixed on main by #634 (243f9d678, merged d2f3346cf). At the merge (f7404d3c6), main's version was kept and this branch's (6244dc472) dropped.
- components-live T14 failed on four heads in a row (744dbc9b9, 348e466ee, 6244dc472, f7404d3c6) but never locally. Cause: the new stale-part rule in `settle` relabels a part at an older generation with a closed gate's phase, and it fired before any part had acted. Analytics has one part (the API's loops), so the component read off while its row still said running, then draining. A first fix, skipping a draining part (b469e4db5), was not enough: the relabel came before the part had reported at all. Root fix (899b0ee2d): the rule applies only once another part of the component has reported at the new generation; a part that is alone, or draining, keeps its word until it reports. The unit test fails on the old code.
- Main was merged three times (744dbc9b9, f7404d3c6, 225a4b1e5). Conflicts were in runner_test.go (main's kept) and in LibraryAdmin.tsx and useLibraryBoard.ts (both sides kept). The branch adds no migration: 0182 is #634's and 0183 is #628's.

## Keep doing
- Meet the test budget at the cause (shared fixtures, single transactions), not by raising caps or splitting tests.

## Open
- The TestYtDlp_* tests take about 1.0 s each under -race locally, near the 1.2 s hard cap.
