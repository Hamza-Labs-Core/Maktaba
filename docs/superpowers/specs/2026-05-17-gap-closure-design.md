# Gap-Closure Program — Design Spec

**Date:** 2026-05-17
**Status:** Approved (brainstorming) — pending spec review
**Execution policy:** auto-continue (no Wave-0 checkpoint), single final
PR, no separate Wave-3 gate. W0-V (real test gates) merges before any
other track — non-negotiable.
**Source of truth:** `specs/gap-analysis/` (25 epic reports + `MASTER.md`, evidence cited to `file:line`)
**Tracking:** Linear, HamzaLabs / Maktaba project — 162 `[Gap]` issues (26 P1, 39 P2, 97 P3)

## 1. Problem

A behavioral gap analysis scored ~1,410 leaf acceptance criteria across 25
epics; only ~17% are complete-and-wired (vs ~63% on the prior structural
audit). The 46-point gap is "code exists" vs "code runs." Four systemic
wiring failures cascade through ~17 of 25 epics. A naive "spin max agents,
fix all issues, one branch" approach fails because (a) the issues form a
dependency chain, not independent units; (b) parallel agents on one working
tree corrupt each other; (c) the CI e2e/perf merge gates are false-green
(exit 0, no assertions), so agent output cannot be trusted by CI.

## 2. Goal

Close the full P1+P2+P3 gap backlog via dependency-ordered **waves** of
parallel agents, each track isolated in a git worktree, integrated through
a single reviewed `integration/gap-closure` branch with real test gates.

## 3. The 4 systemic root causes (from MASTER.md)

- **R1** — Pipeline dispatch table is all `_placeholder_handler`
  (`pipeline/src/maktaba_pipeline/pipeline/runtime.py:218-235`). Kills
  Epics 01–06, 09. Jobs go `discovered → done` doing nothing.
- **R2** — No gRPC transport. No `shared/proto/`;
  `api/internal/grpcclients/{pipeline,streaming}/realclient.go` return
  `ErrNotImplemented`. Kills Epics 05, 07, 08.
- **R3** — API authn/authz attaches but never requires a principal
  (`auth_bootstrap.go:99`). No `RequireAuth` gate; `CookieAuth` orphaned;
  `GET /api/auth/me` unmounted; minted JWTs carry empty `Lib[]`
  (`auth.go:201,322`). Kills Epics 10, 23; endangers all.
- **R4** — Client↔API contract drift: `web` calls `GET /api/search`
  (server mounts `POST`), unmounted `GET /api/videos/{id}/stream`,
  `GET /api/devices` leaks plaintext `push_token`. Kills Epic 11 flows.

## 4. Wave DAG

### Wave 0 — Systemic trunk (5 parallel tracks, each its own PR)

| Track | Scope | Unblocks | Key issues |
|---|---|---|---|
| W0-R1 | Real pipeline stage→handler dispatch in `runtime.py`/`__main__.py` | Epics 01–06, 09 | HLB-355, 257, 259, 283, 307, 365, 335 |
| W0-R2 | `shared/proto/` + pipeline & streaming gRPC servers; dial from `api` | Epics 05, 07, 08 | HLB-325, 293, 298 |
| W0-R3 | `RequireAuth` gate + `/api/auth/me` + wire `CookieAuth` + populate JWT `lib[]` | Epics 10, 23 | HLB-385, 386, 387, 391, 301, 270 |
| W0-R4 | Web↔API contract: search verb, stream-session handshake, device-token leak | Epic 11 flagship flows | HLB-262, 266, 274, 299, 313 |
| W0-V  | Make `make test-e2e` / `perf-ci` real assertion gates | Safety net for ALL later waves | HLB-383, 327, 397 |

**Ordering within Wave 0:** all 5 dispatched in parallel; **W0-V merges
first**. The other four re-run suites against the real gates before merge.

### Wave 1 — Foundational enablers (parallel, after Wave 0)

- Epic 17 design system (HLB-303, 316, 263) — blocks UI epics 11–14.
- Standalone correctness: `verify.py` hash (HLB-406), `audit_log`
  append-only + partition (HLB-359, 311), idempotency→Postgres
  (HLB-315), license persistence (HLB-287).

### Wave 2 — Per-epic gap-fixing (max fan-out, ≈1 agent/epic)

Epics 01–10 and 18–24, parallel, each a PR. Each agent works its
epic's `epic-NN-*.md` AC table.

### Wave 3 — Reach / greenfield surfaces (tighter briefs, heavier review)

Epics 12 (mobile — no ios/android dirs), 13 (desktop), 14 (TV),
15 (discovery), 16 (subscriptions), 25 (on-prem cloudlink client).
Mostly net-new product; agents weakest here — explicit go/no-go before
dispatch.

## 5. Execution model

### Branch topology

```
main
 └─ integration/gap-closure                 (long-lived; the single reviewable branch)
     ├─ worktree branch: gap/w0-r1-pipeline  (Agent, isolation=worktree)
     ├─ worktree branch: gap/w0-r2-grpc
     ├─ worktree branch: gap/w0-r3-auth
     ├─ worktree branch: gap/w0-r4-contract
     └─ worktree branch: gap/w0-v-testgates
```

- Each track = one `Agent(isolation: "worktree")` on a branch off
  `integration/gap-closure`.
- Track done + tests pass → diff review → merge into
  `integration/gap-closure` → re-run integration suite.
- Wave N+1 worktrees branch from the **updated** integration branch.
- **One final PR** `integration/gap-closure → main` at the very end (no
  per-wave PRs). All waves accumulate on the integration branch.

### Parallelism cap

Max concurrent agents = number of independent tracks in the current wave
(Wave 0 = 5; Wave 2 ≈ one per unblocked epic). Never exceed a wave's
independent-track count — beyond that, agents collide or block.

### Agent brief contents (per track)

- The relevant `specs/gap-analysis/epic-NN-*.md` section.
- Specific `file:line` evidence from the gap doc.
- The ACs to satisfy, with Linear issue IDs.
- Hard constraint: run the real suite, paste actual output, no
  false-green / no "should pass".

## 6. Verification & risk controls

- **W0-V first:** real test gates merge before any other track, because
  current `test-e2e`/`perf-ci` exit 0 asserting nothing — no "tests pass"
  claim is trustworthy until fixed.
- **Per-track gate:** implement → run suite + paste real output → diff
  review for scope creep / conflicting fixes / false-green
  (`superpowers:verification-before-completion`) → merge → re-run
  integration suite.
- **Conflicting fixes grouped:** the 3 auth issues (HLB-385/386/387) are
  one hole → one track, never split across agents.
- **Greenfield quality:** Wave 3 gets smaller briefs + heavier review.
  No separate go/no-go gate (user decision) — runs under the normal
  per-track review like any other wave.
- **DAG drift:** a hidden dependency pauses the wave for re-plan rather
  than merging broken work.
- **No Wave-0 checkpoint (user decision):** waves auto-continue without
  stopping for human quality eval. Consequence: the per-track diff
  review + the real test gates from W0-V are the ONLY quality firewall —
  W0-V merging first is therefore load-bearing, not optional.

## 7. Out of scope

- HLB-3 (Linear onboarding placeholder).
- HLB-252 (already In Progress — left to its owner unless directed
  otherwise).

## 8. Success criteria

- All targeted Linear issues moved to Done with evidence.
- `integration/gap-closure` passes the (now real) e2e + perf gates.
- System is end-to-end functional for the core self-hosted use case
  after Wave 0; product surface complete after Wave 3.
