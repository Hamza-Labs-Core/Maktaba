---
schema: 1
kind: pr
pr: 1135
title: "fix(components): the list says why a component is degraded"
date: 2026-11-10
session: 8a8baae1-1408-434b-b1fc-4b955b55d145
---

## What happened
- A degraded component keeps its Ready condition true: the plugins runner's `Overlay` sets
  `sandbox_partial` on the true Ready condition of `plugins.host` (story 59.07, a NAS kernel
  without Landlock). The components list's `reason` is `store.Status.Reason()`, the first false
  condition's reason, so the Components page showed *Running with problems* with no cause.
- #1135 (merged at 3ac9f39db) adds `degraded_reason` and `degraded_reason_params` to the list,
  the component and each machine (`api/internal/components/service.go`, `degradedReason`): the
  first condition carrying a reason, only for a degraded phase with no false-condition reason.
  `reason` keeps its meaning; the routes stay admin only. OpenAPI (json, yaml, ar) names the
  fields; the web row chip and drawer show the reason in English and Arabic; the
  `sandbox_partial` words became plainer; the plugins sandbox guide (en, ar) says where the
  reason shows. No native app shows the components list.

## What we learned
- `reason` on a component means "why it is not ready". A status that is ready but degraded
  needs its own field; widening `reason` would make clients that treat it as a failure show a
  failure.
- `shared/api/openapi.json` is the file the router coverage test and the portal read;
  `openapi.yaml` and `openapi.ar.json` are kept beside it by hand (the Arabic is keyed by the
  English text, so both change together).

## Keep doing
- Check the code's meaning of a field (its comment, the helper behind it) before choosing
  between widening it and adding one.

## Stop doing
- Setting a reason on a true condition without making sure the list carries it somewhere.

## Open
- None.
