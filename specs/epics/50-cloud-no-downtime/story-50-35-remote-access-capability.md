# Story 50.35 — Remote access is on when any remote path is on

> Epic 50 · Phase 1b · Depends on: Epic 51 (51.09 capabilities, 51.19 licence gating; built) ·
> Needed by: 50.15

## Why

Apps ask the home `GET /api/capabilities` which features are on (Epic 51 story 51.09), and hide
the ones that are off from viewers (51.11). The rule as built
(`api/internal/components/features.go`): a feature is on when **every** component that lists it
is on. The one exception is a conflicts pair that both list it (`livetv.slots`,
`livetv.inprocess`), where either is enough.

`remote_access` is listed by `cloud.link` (the relay agent) and `cloud.relay` (relayed play,
licence flag `cloud_relay`). After 50.19, a Free home has no `cloud_relay`, so `cloud.relay` is
*Waiting* (`licence_missing`) and `remote_access` reads `off`. After 50.15, that same home has
free direct remote access through `cloud.direct`. Its apps would hide remote access that works.

`cloud.relay` and `cloud.direct` don't conflict: a Basic home runs both. Epic 51 has no way to
say "one of these is enough" for a feature, and its catalog loaders refuse an unknown field. This
story adds it.

## Description

### 1. Feature groups in the catalog

A new top-level key in [`shared/components/catalog.toml`](../../../shared/components/catalog.toml),
beside `features` (shown as it is once 50.15 adds `cloud.direct`):

```toml
# members of one group count as one for that feature: either on is enough (story 50.35)
feature_any = { remote_access = [["cloud.relay", "cloud.direct"]] }
```

- Each value is a list of groups; each group lists component ids that all list that feature in
  their own `features`.
- The rule becomes: a feature is on when every component listing it is on, where each group
  counts as one component that is on when any member is on. The conflicts-pair rule stays as it
  is (it is the same rule for a pair that both list a feature).
- `remote_access` is then on when `cloud.link` is on and either `cloud.relay` or `cloud.direct`
  is on. With `cloud.link` off, it is off.
- The admin block names every member when the group is off:
  `"remote_access": {"components": ["cloud.relay", "cloud.direct"], "reason": "licence_missing"}`
  (the first member's reason, as today).

### 2. Loaders and checks

- `shared/components/catalog.go` and `pipeline/src/maktaba_pipeline/components/catalog.py` read
  `feature_any` (both refuse an unknown field today, so both change together), and
  `testdata/catalog.dump.json` gains it; `make catalog-check` keeps them equal.
- The catalog check refuses: a feature not in `features`; an id not in the catalog; a member
  that does not list the feature; an id in two groups of one feature.
- `api/internal/components/features.go` applies the rule. Only desired state and the licence are
  read, as today: whether the direct path's outside check passes is status, and capabilities
  never read status. An app that sees `remote_access: on` still tries its connection list in
  order (50.13).

### 3. ETag and push

No change: the ETag is `c-<revision>-<licence mask>`, and a licence change already moves the
mask, so a Free home buying Basic gets `capabilities_changed` on `/ws/home` as today.

## Acceptance criteria

- **Given** a Free home with `cloud.link` and `cloud.direct` on and no `cloud_relay` flag,
  **when** a viewer reads `GET /api/capabilities`,
  **then** `remote_access` is `on`.
- **Given** the same home with `cloud.direct` turned off,
  **then** `remote_access` is `off`, and an admin sees both `cloud.relay` and `cloud.direct` in `admin.off.remote_access.components`.
- **Given** a Basic home with `cloud.relay` on and `cloud.direct` off,
  **then** `remote_access` is `on`.
- **Given** `cloud.link` off,
  **then** `remote_access` is `off` whatever the group says.
- **Given** a catalog whose `feature_any` names an id that does not list the feature,
  **when** `make catalog-check` runs,
  **then** it fails and names the id.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | `features_test.go` table: link on/off × relay on/off/licence missing × direct on/off | resolve | the rule above in every row |
| T02 | unit (Go, Python) | the catalog | load both | `feature_any` equal in both dumps |
| T03 | unit | bad catalogs in `shared/components/testdata/` (unknown feature, unknown id, member not listing it) | load | refused with the reason |
| T04 | unit | capabilities handler, viewer and admin | GET | viewer: `features` only; admin: the group's members in `admin.off` |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml` (and its pipeline copy via `make catalog-sync`) | `feature_any`. |
| `shared/components/catalog.go`, `catalog_test.go`, `testdata/` | Read and check `feature_any`. |
| `pipeline/src/maktaba_pipeline/components/catalog.py`, its tests | The same. |
| `api/internal/components/features.go`, `features_test.go` | The rule. |

### Tests

T01–T04 in the Go and Python unit jobs (the `catalog` job runs T02 and T03).

### Rollout

This story ships the key, the loaders, the checks and the rule, with `feature_any = {}` (the
check refuses an id the catalog does not have). 50.15 adds the `remote_access` group in the
same change as `cloud.direct`. Until then every answer is as today.

### Rollback

Revert. A Free home then reads `remote_access: off` again; nothing is stored.

### Dependencies

Epic 51 (51.09, 51.19; built). Needed by 50.15.
