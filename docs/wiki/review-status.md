# Review Status

> State of every spec / plan review. All blockers and majors are **resolved**; reviews are preserved for historical traceability.


## At a glance

| Review | Scope | Plans/docs | Blockers | Majors | Status |
|--------|-------|-----------:|---------:|-------:|--------|
| [REVIEW.md](../../specs/REVIEW.md) | architecture.md + 4 epic docs | 5 docs | 18 | 24 | RESOLVED — see §7 build order, §8 recommendations |
| [PLAN_REVIEW.md](../../specs/PLAN_REVIEW.md) | Epics 01–06 (Pipeline) | 49 plans | 17/17 | 18/18 | RESOLVED |
| [PLAN_REVIEW_07_13.md](../../specs/PLAN_REVIEW_07_13.md) | Epics 07–13 (API, Streaming, Library, Auth, Web, Mobile, Desktop) | 105 plans | 22/22 | 39/39 | RESOLVED (2026-05-04) |
| [PLAN_REVIEW_14_17.md](../../specs/PLAN_REVIEW_14_17.md) | Epics 14–17 (TV, Discovery, Subscriptions, UX Design) | 33 plans | 13/13 | 24/24 | RESOLVED |
| [PLAN_REVIEW_18_24.md](../../specs/PLAN_REVIEW_18_24.md) | Epics 18–24 (NFR: Performance, Scalability, Testing, Observability, DevOps, Security, Data Integrity) | 57 plans | 24/24 | 61/61 | RESOLVED |

Format: `raised/resolved` for each severity column.


## REVIEW.md (top-level architecture review)

> Audit of `specs/architecture.md` against the four epic documents. Identifies cross-document conflicts, missing integrations, and dependency sequencing.


**Issues by document (originally raised):**

| Document | Blocker | Major | Minor | Total |
|----------|--------:|------:|------:|------:|
| arch | 4 | 5 | 7 | 16 |
| 01 (pipeline) | 3 | 4 | 4 | 11 |
| 02 (API/streaming) | 6 | 8 | 9 | 23 |
| 03 (clients/discovery) | 3 | 2 | 5 | 10 |
| 04 (NFR) | 2 | 5 | 6 | 13 |
| **Total** | **18** | **24** | **31** | **73** |

**Status.** All 11 must-fix items in §8 have been resolved. Major and minor cleanups landed across the 24-epic spec.


**Key resolutions:** content-hash uniqueness scope, transcripts UNIQUE constraint + `is_active`, subtitle_files.is_embedded column, transcripts_fts source-of-truth (transcript_units), audit-table consolidation (single `audit_log` with `category`), JWT `library_ids` claim, missing API stories (recommendations, devices/register, auth/pair).


See `specs/REVIEW.md` §7 for the canonical build order — also reproduced in [build-order.md](build-order.md).

## PLAN_REVIEW.md

**Scope.** Epics 01–06 (Pipeline)  
**Plans reviewed.** 49  
**Status.** RESOLVED


| Severity | Raised | Resolved | Remaining |
|----------|-------:|---------:|----------:|
| Blockers | 17 | 17 | 0 |
| Majors | 18 | 18 | 0 |
| Minors | many | many | some by-design |

**Summary.** Migration-slot conflicts (7), naming-convention violations (3), code-blocker bugs (10), cross-plan ownership conflicts (3), schema deviations vs architecture §8.1 — all fixed. Migration slots now tracked centrally in shared/db/migrations/MANIFEST.md.


**Resolution artifact.** shared/db/migrations/MANIFEST.md; architecture.md §8.6


Source: [specs/PLAN_REVIEW.md](../../specs/PLAN_REVIEW.md)

## PLAN_REVIEW_07_13.md

**Scope.** Epics 07–13 (API, Streaming, Library, Auth, Web, Mobile, Desktop)  
**Plans reviewed.** 105  
**Status.** RESOLVED (2026-05-04)


| Severity | Raised | Resolved | Remaining |
|----------|-------:|---------:|----------:|
| Blockers | 22 | 22 | 0 |
| Majors | 39 | 39 | 0 |
| Minors | 47 | most | minor |

**Summary.** Schema drift (Epics 07/08/09), ID type drift (BIGSERIAL vs UUID), table-name drift, FSM state casing, gRPC contract drift vs §9.9, subtitle_gen stage owner, devices-table double-ownership, audit_log category enum, refresh_tokens.device_id, device-pat auth source, Web UI calling endpoints not in Epic 07, Tauri 2 ACL capabilities, migration ordering. Architecture.md updated as canonical, then 105 plans swept.


**Resolution artifact.** git log claude/cool-lichterman-f978aa


Source: [specs/PLAN_REVIEW_07_13.md](../../specs/PLAN_REVIEW_07_13.md)

## PLAN_REVIEW_14_17.md

**Scope.** Epics 14–17 (TV, Discovery, Subscriptions, UX Design)  
**Plans reviewed.** 33  
**Status.** RESOLVED


| Severity | Raised | Resolved | Remaining |
|----------|-------:|---------:|----------:|
| Blockers | 13 | 13 | 0 |
| Majors | 24 | 24 | 0 |
| Minors | 28 | most | by-design |

**Summary.** Two double-ownership bugs (recommendations endpoint, pairing endpoint) where two plans defined the same route with incompatible schemas — now resolved. Three plans shippable as-is: 17-03 motion, 17-04 loading-states, 17-05 error-empty-states.


**Resolution artifact.** Resolution log §A in PLAN_REVIEW_14_17.md


Source: [specs/PLAN_REVIEW_14_17.md](../../specs/PLAN_REVIEW_14_17.md)

## PLAN_REVIEW_18_24.md

**Scope.** Epics 18–24 (NFR: Performance, Scalability, Testing, Observability, DevOps, Security, Data Integrity)  
**Plans reviewed.** 57  
**Status.** RESOLVED


| Severity | Raised | Resolved | Remaining |
|----------|-------:|---------:|----------:|
| Blockers | 24 | 24 | 0 |
| Majors | 61 | 61 | 0 |
| Minors | many | addressed | by-design |

**Summary.** Schema canonicalization, state casing, audit_log ownership (plan-21-06 sole creator; plan-09-17 superseded), perf budgets, admin-port mux, top-level Go module, Capacitor field, ghost binaries — reconciled. Epic 23 ↔ Epic 10 contradictions resolved with explicit canonical ownership: Epic 23 wins on authz (three-role) + rate-limit numbers (with 10-12's library); Epic 10 wins on HSTS placement + JWT key storage. Architecture §3 (lowercase states), §9.9 (Streaming superset), new §11.6 (telemetry block) updated.


**Resolution artifact.** Inline resolution log per cross-cutting bullet


Source: [specs/PLAN_REVIEW_18_24.md](../../specs/PLAN_REVIEW_18_24.md)

---

## Remaining work

- All 🔴 **blockers** raised across all five reviews are resolved.
- All 🟠 **majors** raised across all five reviews are resolved.
- 🟡 **minors** noted as "by-design" or "out of scope for the review" remain in the original review files; the rest are addressed.
- No open audit findings block implementation work.
