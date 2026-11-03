# Story 51.19 — Licence gating

> Epic 51 · Phase 3 · Depends on: 51.08 · Needed by: —

## Why

Licences already gate one thing: remote access through the cloud relay. The gate is a
middleware, `subscriptions.RelayGate` (`api/internal/subscriptions/gate.go`), which answers
`403` (problem type `…/problems/license-required`) to every relayed request on a home whose
licence does not grant `cloud_relay`. Nothing else knows: the Components page (51.10) would show
"Remote access: Running" on a free home whose every remote request is refused, and an admin can
switch it on and see nothing happen.

This story gives a component **one licence flag** in the catalog and makes the API, the runners
and the page agree on it. It adds no paywall (H10): only `cloud.relay` is gated, by the same
flag as today.

## Description

### 1. The catalog field

`licence` names **one existing flag key** from `api/internal/flags/declarations.go` whose
declaration has a `Feature` (a licence feature of `api/internal/subscriptions/subscriptions.go`):
`cloud_relay`, `multi_user`, `cloud_metadata_backup`, `advanced_analytics`, `federation`.
`flags_admin` and `early_access` have no `Feature` and are refused. Empty means free in every
tier. The licence is read through `subscriptions.Store.Allows(feature)`: the licence, not the
flag overrides in `feature_flag_overrides` (an admin cannot unlock a plan feature by flipping a
flag).

A new check in the `catalog` lint job (51.01): every non-empty `licence` is a declared flag with
a `Feature`. Today exactly one component sets it:

```toml
[[component]]
id       = "cloud.relay"
kind     = "loop"
loops    = ["cloudlink_reporter", "relay_gate"]
requires = ["cloud.link"]
licence  = "cloud_relay"

[[component]]
id        = "cloud.link"
kind      = "process"
processes = ["cloudlink"]           # maktaba-cloudlink: claim, identity, health
licence   = ""
```

**Why `cloud.link` is split out.** The relay agent (`maktaba-cloudlink`) also does the claim
and writes the identity file the licence refresher needs to fetch a plan licence
(`api/license.go`: `cloudlink.IdentitySource`, `ServerID`). Gating the agent would lock a home
out of the very licence that unlocks it. So the agent stays free (and runs exactly when it runs
today, per `[cloud]`, F15), and what is gated is what the licence pays for: serving relayed
traffic and the cloudlink reporter. A free home's relayed requests are refused as today.

### 2. The API refuses to enable without the licence

`PUT /api/components/{id}` with `want = enabled` (51.08), a bundle apply (51.17) or a CLI
`components enable` on a component whose licence is not granted answers:

```http
HTTP/1.1 403 Forbidden
Content-Type: application/problem+json

{ "type": "https://maktaba.tv/problems/license-required",
  "title": "Not included in this home's plan", "status": 403,
  "code": "licence_missing", "component": "cloud.relay",
  "feature": "cloud_relay", "tier": "free", "required_tier": "home" }
```

It is `subscriptions.Refusal(feature, tier)` with `code` and `component` added, so clients that
already read `license-required` keep working. Order: the role check runs first (`403
admin-only` for a non-admin), so a viewer never learns the plan. A bundle apply that includes a
gated component refuses the whole batch (no half-applied bundle) and lists the components.

`want = installed` and `want = absent` are always allowed: installing or removing files needs no
plan.

### 3. A lapsed licence: Waiting, the wish kept

The API is the one writer of desired state (H13), so it publishes what the licence allows on
each desired row: `licensed bool` and `licensed_until timestamptz` (the licence's
`expires_at + SkewGrace`, null when not granted). It rewrites them when the licence changes:
the refresher's apply (`subscriptions/refresh.go`), `license apply`, a revocation, and the
`license_reload` loop (`api/main.go`), each followed by `NOTIFY components_changed`.

Every runner (51.04–51.06) computes the `Licensed` condition from those two fields and the
clock: `licensed AND (licensed_until IS NULL OR now < licensed_until)`. False means phase
**Waiting**, reason `licence_missing`, and the component is stopped like any disable (§4.5:
stop, drain, park). **`want` is not touched.** When the licence comes back, `Licensed` turns
true on the next `NOTIFY` (≤ 2 s, N1) or poll (≤ 30 s) and the component starts with nobody
touching it.

Because `licensed_until` is on the row, a runner whose API is down still stops a component at
the right time, and a home offline from the cloud keeps the grace the licence store already
gives (`subscriptions.SkewGrace`, 72 h past a licence that lives 31 days and is refreshed
daily). **This story adds no grace and changes none.**

For `cloud.relay` the API's own runner (51.05) does it: `RelayGate` asks the component's
`Licensed` condition instead of the store directly (same answer, one source), and the reporter
loop is stopped while Waiting.

### 4. What people see

- **Components page (admin).** The phase *Waiting* with the reason's words and a link to
  Settings → Subscription. The enable switch on a gated component without the licence is shown
  with a lock and the plan it needs; pressing it shows the `licence_missing` words.
- **Capabilities (51.09).** `remote_access` is `off` while `cloud.relay` is Waiting; viewers'
  apps hide it, admins' apps explain it.
- **Audit.** A refusal is not a change and writes no `audit_log` row; the condition's change is
  in `component_status` with its time.

Words (English + Arabic, same PR; web, Android and Apple admin screens):

| Key | English | Arabic |
|---|---|---|
| `components.reason.licence_missing` | Needs the {required_tier} plan. This home is on {tier}. | يتطلب خطة {required_tier}. هذا الخادم على خطة {tier}. |
| `components.licence.refused` | This part is not in this home's plan. Apply a licence in Settings → Subscription. | هذا الجزء غير مشمول في خطة هذا الخادم. طبّق ترخيصًا من الإعدادات ← الاشتراك. |
| `components.licence.waiting` | Paused until the plan includes it again. Your choice is kept. | متوقف حتى تشمله الخطة مرة أخرى. اختيارك محفوظ. |
| `components.licence.locked` | Needs a plan | يتطلب خطة |
| `components.licence.openSubscription` | Open Subscription | فتح الاشتراك |

## Acceptance criteria

- **Given** a free home,
  **when** an admin enables `cloud.relay`,
  **then** the API answers `403` with `code = licence_missing`, `feature = cloud_relay`,
  `required_tier = home`, and the desired row is unchanged.
- **Given** a Home-tier home with `cloud.relay` Running,
  **when** the licence expires past `SkewGrace`,
  **then** `cloud.relay` goes to Waiting (`licence_missing`), relayed requests get today's
  `403 license-required`, `want` stays `enabled`, and `cloud.link` keeps running.
- **Given** that Waiting component,
  **when** a valid licence is applied,
  **then** it is Running within 30 s with no admin action.
- **Given** the API is stopped,
  **when** `licensed_until` passes,
  **then** the runners still stop the component.
- **Given** a viewer, a cloud-linked play sign-in or no sign-in,
  **when** it tries to enable a gated component,
  **then** it gets 403 `admin-only` / 403 `admin-only` / 401, never `licence_missing`.
- **Given** a catalog entry with `licence = "early_access"`,
  **when** the `catalog` lint runs,
  **then** it fails.
- **Given** a free home with `[cloud] enabled` before this epic,
  **when** it updates to the release with this story,
  **then** `maktaba-cloudlink` still runs (51.03's scenario 12 diff passes).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | API, free store | `PUT /api/components/cloud.relay` want enabled as admin | 403 `licence_missing`, body fields as §2; row unchanged |
| T02 | unit | Home-tier store | same | 200; generation bumped |
| T03 | role | API | same call as no one / viewer / play sign-in (`scp: play`, `is_admin`) | 401 / 403 `admin-only` / 403 `admin-only` |
| T04 | unit | free store | `want = installed`, `want = absent` | allowed |
| T05 | unit | bundle apply including `cloud.relay` on free | apply | whole batch refused, list names it |
| T06 | unit | runner, fake clock, `licensed_until` = now + 1 min | advance 2 min | `Licensed` false, Waiting, `want` unchanged |
| T07 | unit | refresher applies a licence | event | rows rewritten, `NOTIFY` sent once |
| T08 | integration | scratch Postgres, API + runner | expire, then apply licence | Waiting, then Running ≤ 30 s |
| T09 | unit | `RelayGate` | relayed request while Waiting | today's 403 problem (golden body unchanged) |
| T10 | lint | catalog with `licence = "early_access"` / unknown key | `catalog` job | fails |
| T11 | unit (web, Android, Apple) | i18n key tests | — | every key in §4 has en and ar |

## Edge cases

- **A flag override turns `cloud_relay` on for a free home.** No effect: the gate reads the
  licence (`Store.Allows`), not overrides.
- **Revocation.** `StatusRevoked` sets `licensed = false` at once; the component stops within
  the NOTIFY latency.
- **A licence downgrade from Pro to Home.** `cloud_relay` is in both; nothing changes.
  `federation` would gate if a component ever named it.
- **Clock wound back on the home.** Same exposure as the licence store today; `licensed_until`
  copies its rule, no new one.
- **A joined machine.** Gating is per home: machines read the home's desired rows. Today no
  gated component runs on a machine (`cloud.relay` is home-only).
- **Older apps** see the same `403 license-required` for relayed requests as before.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml` (from 51.01) | `cloud.link` split from `cloud.relay`; `licence = "cloud_relay"`. |
| `api/internal/flags/catalog_licence_test.go` (new) | the catalog rule: declared flag with a `Feature` (run by the `catalog` job). |
| `api/internal/handlers/components/` (from 51.08) | licence check after the role check on enable and bundle apply; the 403 body. |
| `api/internal/subscriptions/gate.go` | `RefusalFor(component, feature, tier)` adds `code`, `component`; `RelayGate` reads the component condition. |
| `api/internal/subscriptions/refresh.go`, `api/main.go` (`license_reload`) | on every licence change, rewrite `licensed` / `licensed_until` and `NOTIFY components_changed`. |
| `shared/db/migrations/` (new file, next number at build time) | `components.licensed`, `components.licensed_until` (additive). |
| `api/internal/components/runner/` (from 51.05) and the supervisor / pipeline runners (51.04, 51.06) | the `Licensed` condition from the row and the clock. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | §4 words. |
| `apps/android/core/src/main/res/values/strings.xml`, `apps/android/core/src/main/res/values-ar/strings.xml` | reason and refusal words. |
| `apps/apple/Player/Localizable.xcstrings`, `apps/apple/Remote/Localizable.xcstrings` | same. |
| `shared/api/openapi.yaml` | the 403 body on the component routes. |

### Tests

- T01–T07, T09 Go unit (`api`); T03 is the role test of every enable path (PUT, bundle, the CLI's
  route); T08 Go integration job (scratch Postgres).
- T10 in the `catalog` lint job; T11 in the web, Android and Apple unit jobs.
- **CI only:** none beyond the above; scenario 12 (51.03) covers "the agent still runs".

### Rollout

Ships with the rest of Phase 2's API. A free home sees `cloud.relay` as Waiting with words
instead of Running; nothing it did before stops working.

### Rollback

The licence check is one branch in the component handlers and one condition in the runners;
reverting the story's PR restores "every component is free". `RelayGate` keeps its own store
check as a fallback when the component runner is off (`[components] reconcile = off`), so remote
access is never opened by a rollback.

### Dependencies

51.08 (the admin API and its role tests). Uses 51.04–51.06's runners and 51.09's capabilities.
