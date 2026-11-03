# Story 50.20 — The Broadcaster tier and guest sharing: plans, viewer-hour metering, billing

> Epic 50 · Phase B · Depends on: 50.19, Epic 51 (51.19 licence gating, built) · Needed by: 50.22, 50.26, the
> broadcasting gate (README §14.11)

## Why

Broadcasting to people outside the home costs Maktaba things remote play doesn't:

- always-on uploads to the edge (about $7–14 a channel a month);
- viewer-hours;
- moderation and legal exposure.

The plans of 50.19 have no place for it. README §14.4 proposes two things:

- **guest sharing** inside Pro;
- a **Broadcaster** add-on on top of Pro.

Both follow decision 4's rules: an allowance, then optional pay-as-you-go with a cap the user
sets, then lower quality. A viewer who is playing is never stopped by a limit. The numbers and
prices are the owner's (README §14.10, questions 1–7), so they live in the catalog as `TBD` until
set.

**We charge for infrastructure, not content** (decided 2026-11-03). Every unit here is a unit of
infrastructure: channels carried, concurrent viewers served, viewer-hours delivered, and the
ladder's bandwidth. No price depends on what airs. **Viewers never pay.** The only money flow is
from the publisher to Maktaba.

## Description

### 0. The Broadcaster agreement at purchase

Buying Broadcaster, or turning on Pro's guest sharing for the first time, asks the account owner
to accept the short **Broadcaster agreement** (README §14.6), shown in English and Arabic:

- Maktaba Cloud provides infrastructure;
- the publisher is responsible for having the rights to what they air;
- Maktaba acts on notices and on actual knowledge, and can take channels off air;
- the repeat-infringer policy applies.

Acceptance is stored in `broadcast_agreements (account_id, version, accepted_at, ip_hash)`. A new
version is asked for at the next publish. The API refuses to publish without a current acceptance
(`409 broadcast_agreement_required`).

### 1. The catalog (extends `cloud/internal/billing/plans.go`)

```go
// BroadcastTier is what a home may publish. Nil on Free and Basic.
type BroadcastTier struct {
    ID                     string  // "guest" (inside Pro), "broadcaster" (add-on)
    MaxChannels            int     // 1 / 3
    Access                 []string // {"invite"} / {"invite","open"} (viewers never pay)
    OnDemandOnly           bool    // true for guest sharing
    MaxConcurrentViewers   int     // 5 / 100 (per publisher, all channels)
    ViewerHoursPerMonth    int     // 50 / 5000
    MaxRungs               int     // 2 / 3
    MaxTopKbps             int     // 3000 / 10000
    PAYG                   bool    // false / true
    FairUseMultiplier      float64 // 0 / 2.0 (lowest rung up to 2× the allowance)
    PriceCents             *int    // nil = TBD; guest = included in Pro
    PAYGPricePer100HCents  *int    // nil = TBD
    ExtraChannelPriceCents *int    // nil = TBD
    ExtraViewers100Cents   *int    // nil = TBD
    ProviderPriceIDs       ProviderBroadcastPrices // from config, per environment
}
```

- `Tier` (50.19) gains `Broadcast *BroadcastTier`. Free and Basic have `nil`, and Pro has the
  `guest` tier.
- Broadcaster is a **subscription add-on** that needs Pro. Buying it sets
  `servers.broadcast_plan = 'broadcaster'`. Dropping Pro drops Broadcaster at the same renewal.
- `TBD` values block the broadcasting gate. That check is in 50.19 T12's launch switch, extended
  to the broadcast catalog.
- **The home's licence flag.** The home learns what its plan allows through the signed licence
  (Epic 16, story 25.26), and Epic 51 gates a component by one flag of
  `api/internal/flags/declarations.go` (story 51.19). This story adds the flag **`broadcast`**
  (`subscriptions.FeatureBroadcast = "broadcast"` in `api/internal/subscriptions/subscriptions.go`,
  declared with `Feature` set, as `cloud_relay` is). The cloud's licence carries it for Pro
  (guest sharing) and for Broadcaster; Free and Basic don't. The home's `livetv.broadcast`
  component (50.22) names it as its `licence`, so on a home without it the component is
  *Waiting* with reason `licence_missing` and turning it on answers `403 licence_missing`. How
  much a home may publish (channels, viewers, hours) stays the cloud's and the edge's to enforce
  (§2); the flag only says whether the home may publish at all.

### 2. The mode for a new viewer session

The edge (50.26) asks for a **broadcast mode** when a viewer starts a session. It reads the
publisher's plan state, which the edge holds in `PublisherControl`. The cloud pushes that state
on every change and every 5 minutes.

| Mode | When | The edge does |
|---|---|---|
| `full` | within the allowance, or within pay-as-you-go's cap | every rung the channel publishes |
| `lowest` | past the allowance and the cap, below the fair-use ceiling (Broadcaster only) | the master playlist for **new** sessions lists only the lowest rung |
| `wait` | past the fair-use ceiling (Broadcaster), or past the allowance (guest sharing), or at the concurrent-viewer limit | `429 broadcast_full` for new sessions; the page shows `broadcast.full` with a retry |
| `off` | no plan, a kill-switch entry (50.25), or a takedown (50.24) | `403` / `451`; existing sessions stop at the next playlist |

- **Playing sessions keep their mode** until they end. A mode change applies only to new
  sessions, so nobody's picture drops mid-programme because a counter ticked over. The exception
  is `off`, which stops playing sessions too.
- The concurrent-viewer limit counts sessions with a heartbeat in the last 150 s, per publisher,
  in `PublisherControl` (50.26).

### 3. Metering

- **A viewer-minute** is one 60 s heartbeat of a session in `full` or `lowest`. The edge counts
  them in `PublisherControl` (exact, single writer per publisher) and also writes them to
  Analytics Engine (50.28) as a cross-check.
- Every 5 minutes the edge posts the counts to `api.maktaba.tv/_edge/usage` (signed with the
  edge's key, idempotency key `<publisher>:<window>`), into `broadcast_usage_monthly
  (publisher_id, month, minutes_full, minutes_lowest, minutes_payg)`.
- **If the cloud API is down**, the counts stay in the DO and are posted later with the same keys.
  The DO keeps 7 days of windows.
- **Order of spending** (Broadcaster):
  1. the allowance;
  2. pay-as-you-go, if it is on, until its cost reaches the user's cap;
  3. `lowest` until 2× the allowance;
  4. `wait`.

  Guest sharing: the allowance, then `wait`.
- The month boundary is 00:00 UTC on the 1st, the same as 50.19's `MonthStart`.

### 4. Billing (same `billing.Provider`, TBD)

| Item | Provider object | How |
|---|---|---|
| 4. Broadcaster | a recurring flat price | added to the Pro subscription from the Billing page |
| 5. Pay-as-you-go viewer-hours | a metered price `broadcast_viewer_hours` | hourly usage reports of `minutes_payg / 60`, idempotency key `<publisher>:<month>:<hour>`, clamped to the cap; the month's last report at 00:30 UTC on the 1st (as 50.19 §6) |
| 6. Extra channels | a per-unit price | quantity on the Billing page; the provider prorates |
| 7. Extra 100 viewers | a per-unit price | the same |

Daily reconciliation compares `minutes_payg` with what the provider recorded. A mismatch over
60 viewer-minutes raises a SEV3. Nothing in this story creates a price at any provider.

### 5. What the publisher sees

On the Billing page (`web/cloud`) and the home's Broadcast page (50.22):

- this month's viewer-hours against the allowance;
- pay-as-you-go spent against the cap;
- the current mode;
- concurrent viewers against the limit.

At 80 % and 100 % of the allowance and of the cap, the publisher gets an email and an in-app
notice.

## Acceptance criteria

- **Given** a Free or Basic home,
  **when** its admin opens Live TV → Broadcast,
  **then** publishing is offered only as an upgrade (`broadcast.needsPro`), and the cloud API
  refuses `POST /v1/broadcast/channels` with `402 broadcast_needs_plan`.
- **Given** a Pro home without Broadcaster,
  **when** it publishes,
  **then** only one channel, `invite` access and on-demand mode are allowed, and a second channel
  gets `402 broadcast_limit` naming `max_channels`.
- **Given** a Broadcaster publisher at 5,000 viewer-hours with pay-as-you-go off,
  **when** a new viewer starts,
  **then** the master playlist lists only the lowest rung, and viewers already playing keep
  theirs.
- **Given** the same publisher at 10,000 viewer-hours,
  **when** a new viewer starts,
  **then** the viewer gets `429 broadcast_full`, and nobody playing is stopped.
- **Given** pay-as-you-go on with a cap of 20 (currency units),
  **when** its cost reaches 20,
  **then** new sessions get `lowest`, the publisher is told, and the provider is never sent more
  than the cap's worth.
- **Given** the edge posts the same 5-minute window twice,
  **when** the API receives it,
  **then** it is counted once.
- **Given** the cloud API is down for 2 hours,
  **when** it returns,
  **then** the queued windows arrive and the month's total is exact.
- **Given** an account that hasn't accepted the current Broadcaster agreement,
  **when** its home publishes a channel,
  **then** the API answers `409 broadcast_agreement_required`, and nothing is published until it
  is accepted.
- **Given** any broadcast catalog value is `TBD`,
  **when** broadcasting is switched on,
  **then** the switch is refused with the missing field named.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | mode function | guest / Broadcaster: within allowance, PAYG, past cap, past 2×, at concurrency limit, revoked | `full` / `full` / `lowest` / `wait` / `wait` / `off` |
| T02 | unit | catalog | Free / Basic / Pro / Pro + Broadcaster | `nil` / `nil` / guest / broadcaster |
| T03 | unit (edge, vitest) | `PublisherControl` | 1,000 heartbeats across sessions; a session in `lowest` | minutes counted per mode; playing sessions keep their mode after a change |
| T04 | integration | API + fake edge | duplicate window, out-of-order windows, a 2 h API outage | counted once; total exact |
| T05 | integration | fake payment provider | hourly PAYG reports, a retry, a provider outage | one record per hour; clamped to the cap; queued and sent later |
| T06 | integration | fake payment provider | buy 2 extra channels, 1 extra 100 viewers | limits 5 channels / 200 viewers in the edge within 5 min, within 1 s with a push |
| T07 | unit | launch switch | catalog with a `TBD` | refused, naming the field |
| T08 | role test | API | a non-owner home user calls the broadcast billing endpoints | `403` (APIs enforce roles) |
| T09 | Arabic | every new key | strings check | Arabic present in `web/cloud` and home web `ar.json`, Android `values-ar`, the Apple catalog |
| T11 | integration | API | publish without / with the current agreement; a new agreement version | 409 / 201 / 409 until re-accepted |
| T10 | rehearsal (CI, 50.29 `broadcast-plans`) | edge in Miniflare + cloud stack | drive a publisher across every mode | modes in order; no playing session stopped except on `off` |

## Edge cases

- **The publisher downgrades from Broadcaster to Pro mid-month.**
  - Channels beyond one are unpublished at the period's end, not at once.
  - The publisher picks which one stays. If they don't, the oldest stays.
  - An `open` channel becomes `invite` at the same time, and the old links ask for an invite.
- **Pro lapses** (the provider gave up retrying). Broadcasting goes `off` with Pro, the same rule
  as 50.19: no plan, no relay and no broadcast. Viewers see `broadcast.offAir`.
- **Two homes on one account.** Plans are per home (`servers.broadcast_plan`), as 50.19's are, and
  the publisher handle is per account. A second home needs its own Broadcaster.
- **Month rollover during a session.** The session stays in its mode. New sessions start in
  `full` again.
- **Clock skew.** The usage month is the API database's clock at insert. The edge's window ids
  are its own UTC minutes.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `billing.plan.broadcaster` | Broadcaster | البث |
| `billing.plan.broadcaster.desc` | Publish up to {channels} of your channels to people outside your home: up to {viewers} viewers at once and {hours} viewer-hours a month, in up to 1080p. | انشر حتى {channels} من قنواتك لأشخاص من خارج منزلك: حتى {viewers} مشاهد في الوقت نفسه و{hours} ساعة مشاهدة شهريًا، بجودة تصل إلى 1080p. |
| `billing.plan.pro.guestSharing` | Guest sharing: invite up to {viewers} people to watch one of your channels, {hours} viewer-hours a month. | مشاركة الضيوف: ادعُ حتى {viewers} أشخاص لمشاهدة إحدى قنواتك، {hours} ساعة مشاهدة شهريًا. |
| `broadcast.needsPro` | Sharing channels with people outside your home needs the Pro plan. | تتطلب مشاركة القنوات مع أشخاص من خارج منزلك الخطة الاحترافية. |
| `broadcast.limit.channels` | Your plan publishes up to {count} channels. Add a channel or unpublish one first. | تتيح خطتك نشر {count} قنوات كحد أقصى. أضف قناة أو أوقف نشر إحداها أولًا. |
| `broadcast.usage.title` | Viewing this month | المشاهدة هذا الشهر |
| `broadcast.usage.hours` | {used} of {allowance} viewer-hours | {used} من {allowance} ساعة مشاهدة |
| `broadcast.mode.lowest` | New viewers watch in lower quality until next month, or until you raise your limit. | يشاهد المشاهدون الجدد بجودة أقل حتى الشهر القادم، أو حتى ترفع حدّك. |
| `broadcast.mode.wait` | New viewers are waiting: this month's viewing limit is reached. People already watching continue. | المشاهدون الجدد في الانتظار: بلغتَ حد المشاهدة لهذا الشهر. من يشاهد الآن يستمر. |
| `broadcast.agreement.title` | Broadcaster agreement | اتفاقية البث |
| `broadcast.agreement.body` | Maktaba Cloud carries your channels; it doesn't choose or own what they show. You are responsible for having the rights to everything you broadcast. We act on copyright notices and on reports, can take a channel off air, and end broadcasting for accounts with repeated valid notices. | تنقل سحابة مكتبة قنواتك، ولا تختار ما تعرضه ولا تملكه. أنت مسؤول عن امتلاك حقوق كل ما تبثه. نتعامل مع إشعارات حقوق النشر والبلاغات، ويمكننا إيقاف بث قناة، وننهي البث للحسابات التي تتكرر بحقها إشعارات صحيحة. |
| `broadcast.full` | This channel is full right now. Try again in a few minutes. | هذه القناة ممتلئة الآن. حاول بعد بضع دقائق. |
| `billing.broadcast.payg.desc` | Keep full quality after your allowance, at {price} per 100 viewer-hours, up to a monthly limit you set. Off unless you turn it on. | حافظ على الجودة الكاملة بعد رصيدك، بسعر {price} لكل 100 ساعة مشاهدة، حتى حد شهري تحدده أنت. متوقف ما لم تفعّله. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/billing/plans.go`, `plans_test.go` | `BroadcastTier`, guest and Broadcaster values, `TBD` checks. |
| `cloud/internal/billing/broadcast_modes.go` (new) | The §2 mode function. |
| `cloud/internal/billing/broadcast_usage.go` (new) | Window intake, monthly totals, PAYG reports, reconciliation. |
| `cloud/internal/billing/provider.go`, `provider_fake.go` | Items 4–7. |
| `cloud/internal/handlers/billing/` | Broadcaster add-on, PAYG cap, extra channels and viewers; webhook for the new items. |
| `cloud/internal/handlers/edge/usage.go` (new) | `POST /_edge/usage` (edge-signed, idempotent). |
| `cloud/internal/handlers/broadcast/agreement.go` (new) | Agreement versions, acceptance, the publish check. |
| `cloud/migrations/` (new, expand-only) | `broadcast_agreements`, `servers.broadcast_plan`, `broadcast_usage_monthly`, `broadcast_usage_windows`, `broadcast_payg_settings`. |
| `web/packages/broadcast-edge/src/publisher.ts` (50.21's package) | Counting, modes, the posting queue. |
| `web/cloud/src/pages/Billing.tsx`, `web/cloud/src/i18n/en.json`, `ar.json` | Broadcaster, guest sharing, usage, PAYG. |
| `web/src/` Broadcast page usage panel, `web/src/i18n/en.json`, `ar.json` | Usage and mode. |
| `cloud/docs/accounts-billing-admin.md` | Broadcaster, metering. |
| `api/internal/subscriptions/subscriptions.go`, `api/internal/flags/declarations.go`, `cloud/internal/license/` | The `broadcast` feature and flag; the licence carries it for Pro and Broadcaster. |

### Tests

- T01–T09 in `relay.yml`'s `test` job (cloud Go) and the edge package's vitest. The payment
  provider is only ever the fake.
- **CI only:** rehearsal scenario `broadcast-plans` (50.29).

### Rollout

1. Expand migration and the catalog with `broadcast.enabled = false`.
2. Edge counting on in staging with admin-granted publishers.
3. The owner sets prices and creates the provider prices (README §12, §14.10 question 4).
4. The broadcasting gate opens Broadcaster.

### Rollback

- `broadcast.plans.enforce = false`: every publisher's new sessions get `full` up to the
  concurrency limit, and counting continues.
- `billing.broadcast_payg.enabled = false` stops new usage reports. Queued ones still go, so
  nothing used goes unbilled.

### Dependencies

50.19 (catalog, `billing.Provider`, the PAYG pattern). 50.21 hosts the counting.
