# Story 50.19 — Plans: Free, Basic and Pro, enforced by the relay and billed by usage

> Epic 50 · Phase 1 · Depends on: 50.04, 50.05 · Needed by: launch gate (README §11), 50.16

## Why

The owner's decisions of 2026-11-03 (README §13, decisions 4 and 6) replace every plan we have
written down so far: the architecture doc's Pro 2 / Family 5 streams, and the code's Free / Pro /
Family with 1 / 4 / 10 streams and 5 / 100 / 500 GiB (`cloud/internal/billing/plans.go`), which
the license calls `free` / `home` / `pro` (`cloud/internal/license/license.go`,
`TierForPlan`) and the cloud web app shows as the "Home" and "Pro" plans
(`billing.plans.homeTier.*`). That is gap G13. Today the relay enforces only a monthly byte cap,
and enforces it by refusing requests (`Meter.Allow` returns `ErrOverLimit`, a `402`); it never
counts streams.

The new rule is that **only the relay is paid**, a paid plan **never cuts remote play off**, and
the relay itself enforces each plan.

## Description

### 1. The plans

| | **Free** | **Basic** (paid) | **Pro** (paid) |
|---|---|---|---|
| Play at home (LAN) | yes | yes | yes |
| Direct remote (50.15) | **yes, free** | yes | yes |
| Relay | **no** | yes, **capped quality** about **3 Mbps** a stream | yes, **full quality** |
| Relayed streams at once | — | **2** | **N** (in the catalog), plus paid extra streams |
| Monthly relay allowance | — | **500 GB fair use, soft**: past it, streams slow further; they never stop | **an allowance in GB** (in the catalog) at full quality |
| Past the allowance | — | slower capped quality (§3) | optional **pay-as-you-go GB** (off by default, a spending cap the user sets); when the allowance and the cap are used up, **Basic's capped quality**, never cut off |
| Extra concurrent streams | — | — | **paid add-on**, per stream a month |
| Price | €0 | **TBD by the owner** | **TBD by the owner** |

- **The Family plan is dropped.** So are family seats (`FamilySeats`).
- The numbers the owner hasn't set yet (Pro's allowance and stream count N, the price of each
  plan, of a pay-as-you-go GB and of an extra stream) live in the catalog with `TBD` values,
  and the launch gate refuses to switch paid plans on while any of them is `TBD` (T12).
- **Why a cap like Plex's**: Plex caps its relay at 2 Mbps ([research.md](research.md) §2). We
  charge for Basic, so its cap is higher (3 Mbps holds 720p well), and Pro is uncapped.

### 2. The catalog (replaces `plans.go`'s tiers)

```go
// cloud/internal/billing/plans.go
const (
    PlanFree  = "free"
    PlanBasic = "basic"
    PlanPro   = "pro"
)

type Tier struct {
    ID                      string
    Relay                   bool    // false on Free
    RelayCapKbps            int     // 3000 on Basic; 0 = full quality (Pro)
    RelaySlowCapKbps        int     // 1500: Basic past fair use, Pro past allowance + cap
    MaxRelayStreams         int     // 2 on Basic; N on Pro
    FairUseBytesPerMo       int64   // 500 GB on Basic (soft)
    AllowanceBytesPerMo     int64   // Pro, full quality; TBD
    PayAsYouGo              bool    // Pro only
    ExtraStreamsAddOn       bool    // Pro only
    PriceCents              *int    // nil = TBD
    PAYGPricePerGBCents     *int    // Pro; nil = TBD
    ExtraStreamPriceCents   *int    // Pro; nil = TBD
    StripePriceID           string  // from config, per environment
    StripePAYGPriceID       string  // metered price on the relay GB meter (§6)
    StripeExtraStreamPriceID string // per-unit price (§6)
    MaxServers              int     // kept; values set with the prices
}
```

- The catalog stays in code (a change is a PR), as today. The values above are the spec;
  `RelaySlowCapKbps = 1500` is this story's choice for "slow further" (half the cap), and the
  owner can change it in the catalog.
- `RelayQoS`, `IncludesTranscoding`, `FamilySeats` and `BandwidthBytesPerMo` go away.
  Transcoding is the home's job and never was a cloud plan feature.

### 3. How the relay enforces each plan

The relay decides a **mode** for every forwarded request from the home's plan state, which it
already caches (50.05) and which `NOTIFY tier_changed` refreshes within 1 s:

| Mode | When | What the relay does |
|---|---|---|
| `refused` | Free plan (or a paid plan that lapsed past Stripe's retries) | Answers `402 relay_needs_plan` without forwarding. The home **still holds its tunnel**: the cloud needs it for direct remote (endpoints, certificates, the outside check), entitlements and push. The app's connection list (50.13) has no `relay` entry for a Free home, so apps try LAN and direct and show `relay.needsPlan`. |
| `full` | Pro, within the allowance or within the pay-as-you-go cap | Forwards untouched. |
| `capped` | Basic within fair use; Pro past its allowance and its cap | Adds `X-Maktaba-Relay-Cap-Kbps: 3000` to the forwarded request and paces media responses (below). |
| `capped_slow` | Basic past 500 GB of fair use; Pro past allowance + cap + 500 GB in `capped` | The same with `1500`. |

**The home transcodes down to the cap.** A request that arrives through the tunnel with
`X-Maktaba-Relay-Cap-Kbps` makes the home's streaming service (HLS) offer only renditions whose
peak bitrate fits the cap (video + audio, with 10 % headroom), transcoding when the file has no
such rendition. Direct and LAN requests never carry the header, so they are never capped.

- **The cost lands on the home.** Each capped relayed stream of a high-bitrate file is a live
  transcode on the home: with a hardware encoder (VideoToolbox, Quick Sync, NVENC, VAAPI) that is
  cheap; in software, a 1080p→720p 3 Mbps transcode takes roughly one to two CPU cores per
  stream. Basic's limit of 2 relayed streams bounds that. The home's own transcode limits and
  placement (Epic 8, Epic 33/36 slots) still apply; when the home can't start a transcode it
  serves the nearest rendition under the cap if one exists, else the app shows
  `relay.homeBusy`. The home's settings say plainly that relayed streams on Basic are
  transcoded (`relay.transcodeNote`).
- **The relay paces as a backstop.** A home that ignores the header (an old release) is held to
  the cap by a token bucket per media stream at the cap plus 20 %, so it slows and never stops.
  Non-media requests (API, artwork, subtitles) are never paced.
- **Concurrent relayed streams.** The relay counts open media streams (HLS sessions) per home in
  `relay_streams` (home, relay, session, opened_at, last_seen_at), written at stream open,
  touched each minute while segments flow, deleted at close, and swept by the worker after 2
  minutes of silence. A new stream past the home's limit (Basic 2; Pro N + its extra streams)
  gets `429 stream_limit`, and the app shows `relay.streamLimit`. Streams already playing are
  never stopped. Direct and LAN streams don't count. In Phase 1 the counting is in Postgres so
  the blue and green processes during a deploy (50.04) agree; 50.16 reuses the same table across
  the pool.
- **The old `402` on a byte cap is removed.** `Meter.Allow` no longer refuses; it returns the
  mode instead. Nothing a paid plan does ends in a cut-off.

### 4. Metering

The existing `Meter` (in-memory per home, flushed every 30 s, buffered through a database blip
by 50.05) is extended:

- Bytes are counted **per mode** (`full`, `capped`, `capped_slow`), into
  `relay_usage_monthly (server_id, month, bytes_full, bytes_capped, bytes_capped_slow,
  bytes_payg)`.
- **Pro's order of spending**: the allowance first (`full`), then pay-as-you-go (if on, `full`,
  counted as `bytes_payg`) until its cost reaches the user's cap, then `capped`, then
  `capped_slow` past 500 GB in capped mode. Basic: `capped` until 500 GB, then `capped_slow`.
- **Switching mode mid-stream** happens at the next request: segments already sent stay, the
  next playlist fetch carries the new cap, and the player steps down a rendition. No stream
  stops.
- **Overshoot.** Across processes (and later relays) a switch can lag by one 30 s flush. Bytes
  in that window are billed at the mode they were counted in, so pay-as-you-go can pass the
  user's cap by at most one flush's worth; the billing job (§6) clamps the reported amount to
  the cap, so the user is never charged past what they set.
- The user sees this month's use, the allowance, pay-as-you-go spent against the cap, and the
  current mode on the Billing page and in the home's remote-access settings, updated every
  minute.

### 5. Pay-as-you-go and the spending cap

- **Off by default.** Only the account owner turns it on, on the Billing page, and must set a
  **monthly spending cap** in the account's currency (no cap, no pay-as-you-go). It can be
  changed or turned off any time; lowering the cap below what's already spent stops further
  pay-as-you-go at once (the spent amount stays billed).
- The relay never asks Stripe whether to allow bytes; the cap is enforced from our own meter,
  so a Stripe outage can't cut anyone off (README §4.3).
- At 80 % and 100 % of the cap the user gets an email and an in-app notice
  (`payg.nearCap`, `payg.capReached`); at 100 % streams drop to capped quality.

### 6. Stripe billing

| What | Stripe object | How |
|---|---|---|
| Basic, Pro | a recurring **flat price** per plan (subscription item 1) | as today: Checkout session (25.13), Customer Portal for changes; price IDs from config. |
| Pay-as-you-go GB | a **usage-based (metered) price** on a Stripe **Billing Meter** `relay_payg_gb` (subscription item 2, added when the user turns pay-as-you-go on, removed when off) | The worker sends **meter events** every hour with the GB of `bytes_payg` since the last report, each with an idempotency identifier `<server_id>:<month>:<hour>` so a retry never double-bills; clamped to the cap (§4). The month's last report runs on the 1st at 00:30 UTC before Stripe finalises the invoice. |
| Extra concurrent streams | a **per-unit price** (subscription item 3), quantity = extra streams | Set on the Billing page; Stripe prorates. Billed per stream a month rather than metered, because a stream slot is a monthly entitlement, not consumption. The relay's limit follows the quantity through the webhook (25.14) and `NOTIFY tier_changed`. |

- Webhooks (25.14) gain `customer.subscription.updated` handling for items 2 and 3, and the
  daily reconciliation compares our `relay_usage_monthly.bytes_payg` with what Stripe recorded
  for the meter; a mismatch over 1 GB alerts (SEV3).
- If meter events can't be sent (Stripe down), they queue in Postgres and are sent later with
  the same identifiers; usage is never lost and never billed twice.
- Prices are **TBD**; nothing in this story creates a Stripe product or price. The owner creates
  them and puts their IDs in the `production` config (README §12).

### 7. Migration from today's code plan names

Today: cloud plans `free` / `pro` / `family` in `users.plan`, `servers.plan` and
`cloud_subscriptions.plan` (CHECK constraints in `00020001_identity.sql`, `00040001_servers.sql`,
`00060001_billing.sql`); license tiers `free` / `home` / `pro`; web strings "Home" and "Pro".

| Old (cloud plan → license tier) | New (cloud plan = license tier) |
|---|---|
| `free` → `free` | `free` (now: no relay; direct remote free) |
| `pro` ($5, 4 streams, 100 GiB) → `home` | `basic` |
| `family` ($10, 10 streams, 500 GiB, 6 seats) → `pro` | `pro` |

- **One expand-only migration** (50.08's rule): widen each CHECK to allow `basic`, then rewrite
  `pro` → `basic` **before** `family` → `pro` in one transaction (the names overlap, so the order
  matters), then a later release drops `family` and the old meaning of `pro` from the CHECKs once
  no running process writes them.
- **The license tier becomes the plan name.** `TierForPlan` is the identity for the three new
  plans; license tier `home` is read as `basic` for 7 days (the entitlement grace, 25.26) so a
  home on an older release keeps working, and homes learn the new names in the next release.
  Whatever else the license already gates on the home keeps its meaning under the new names
  (Basic gets what `home` gave, Pro what `pro` gave), with one change from decision 6: **remote
  access over the direct path is no longer gated on any tier** (50.15 §5).
- **Stripe**: existing subscriptions on the old `pro` / `family` prices are moved to the new
  Basic / Pro prices at their next renewal through the Stripe API from the worker (no charge
  change before renewal); Family's seats end. Paid plans aren't switched on before the launch
  gate, so in practice this touches only test and admin-granted accounts; the admin fleet page
  (25.20) lists every account the migration moved.
- **Strings**: `billing.plans.homeTier.home` / `.pro` and `billing.plans.seats` are removed;
  the new plan strings below replace them.
- The canary home (50.10) moves from its admin-granted Family plan to Pro.

## Acceptance criteria

- **Given** a Free home,
  **when** an app requests anything through the relay,
  **then** the relay answers `402 relay_needs_plan` without forwarding, the home's tunnel stays
  up, and direct remote (when its outside check passes) plays normally.
- **Given** a Basic home and a 20 Mbps file,
  **when** an app plays it through the relay,
  **then** the forwarded request carries `X-Maktaba-Relay-Cap-Kbps: 3000`, the home offers only
  renditions within the cap (transcoding if needed), and the stream's measured rate stays within
  3 Mbps + 20 %.
- **Given** the same file played over direct remote or on the LAN,
  **when** it plays,
  **then** no cap header is present and the original quality plays.
- **Given** a Basic home with 2 relayed streams playing,
  **when** a third device opens a relayed stream,
  **then** it gets `429 stream_limit` and the two playing streams continue.
- **Given** a Basic home past 500 GB this month,
  **when** it keeps streaming through the relay,
  **then** new playlist fetches carry `1500` and nothing stops.
- **Given** a Pro home past its allowance with pay-as-you-go off,
  **when** it streams,
  **then** it drops to capped quality (3000) and never gets a `402`.
- **Given** a Pro home with pay-as-you-go on and a cap of 10 (currency units),
  **when** pay-as-you-go use reaches the cap,
  **then** it drops to capped quality, the user is told, and Stripe is never sent more than the
  cap's worth of GB for the month.
- **Given** the worker sends the same hour's meter event twice,
  **when** Stripe receives them,
  **then** the usage is recorded once.
- **Given** a Pro home with 2 extra streams bought,
  **when** the webhook arrives,
  **then** the relay allows N + 2 relayed streams within 1 s.
- **Given** a database with `pro` and `family` rows,
  **when** the migration runs,
  **then** former `pro` rows are `basic`, former `family` rows are `pro`, and no row changed
  twice.
- **Given** a home on an older release whose license says `home`,
  **when** it checks its entitlement within the grace,
  **then** it behaves as `basic`.
- **Given** any catalog value is `TBD`,
  **when** paid plans are switched on,
  **then** the switch is refused with the missing values named.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | mode function | Free / Basic under & over 500 GB / Pro under allowance, in PAYG, past cap, past cap + 500 GB | `refused` / `capped` / `capped_slow` / `full` / `full` / `capped` / `capped_slow` |
| T02 | unit | relay proxy | capped request | header added; non-media never paced |
| T03 | unit | token bucket | home ignores the header | rate held at cap + 20 %; no error, no close |
| T04 | integration | `relay_streams`, two relay processes | open 3 streams on Basic across both | third `429 stream_limit`; rows swept 2 min after a crash |
| T05 | unit (streaming) | HLS master with renditions 20 / 8 / 3 / 1.5 Mbps | request with cap 3000 | only ≤ 3 Mbps renditions listed; transcode chosen when none fits |
| T06 | unit | meter | bytes across modes and PAYG | `relay_usage_monthly` columns right; PAYG stops at the cap |
| T07 | integration | Stripe test-mode mock | hourly meter events, a retry, a Stripe outage | one usage record per hour; queued events sent later with the same identifier |
| T08 | integration | Stripe mock | buy 2 extra streams | webhook → limit N + 2 within 1 s |
| T09 | integration | migration on a fixture with every old plan | migrate | `pro`→`basic`, `family`→`pro`, nothing changed twice; CHECKs accept the new names |
| T10 | unit (cloud) | `license.TierForPlan` and the home's license reader | old `home` within / past grace | `basic` / refused |
| T11 | Arabic | every new key | strings check | Arabic present in `web/cloud` `ar.json`, home web `ar.json`, Android `values-ar`, Apple catalog |
| T12 | unit | launch switch | catalog with a `TBD` price | refused, naming the field |
| T13 | rehearsal (CI, 50.06 `relay-plans`) | home stack behind the relay, fake Stripe | Free, Basic, Pro over allowance | refused / capped transcode / capped fallback; no stream stops in any |

## Edge cases

- **A plan changes mid-stream.** The next playlist fetch carries the new cap; playing segments
  finish. An upgrade to Pro lifts the cap on the next fetch.
- **A paid plan lapses** (Stripe gave up retrying): the home becomes Free, so `refused`; streams
  already playing finish their current segment and then fail with `relay.needsPlan`. This is the
  one case where relayed play ends, and it is not a plan's limit but no plan.
- **Month boundary.** Counters reset at 00:00 UTC on the 1st (`MonthStart`); a stream in
  `capped_slow` goes back to its plan's normal mode on the next fetch.
- **The home can't transcode at all** (no encoder, CPU saturated): the nearest rendition under
  the cap, else `relay.homeBusy`; the relay's pacing still never stops a stream that is sent.
- **Clock skew between processes**: usage months are taken from the database's clock at flush.
- **Abuse** (one account, many homes): stream limits and allowances are per home, as `servers.plan`
  is today; `MaxServers` limits how many homes an account links.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `billing.plan.free` | Free | مجاني |
| `billing.plan.basic` | Basic | أساسي |
| `billing.plan.pro` | Pro | احترافي |
| `billing.plan.free.desc` | Play at home, and from anywhere directly when your router allows it. No relay. | شغّل في المنزل، ومن أي مكان مباشرةً عندما يسمح جهاز التوجيه بذلك. بلا وسيط. |
| `billing.plan.basic.desc` | Remote play through the Maktaba relay when a direct connection isn't possible, at up to about 3 Mbps (good 720p), on up to 2 devices at once. | التشغيل عن بُعد عبر وسيط مكتبة عندما لا يتاح الاتصال المباشر، بجودة تصل إلى نحو 3 ميغابت في الثانية (720p جيدة)، على جهازين كحد أقصى في الوقت نفسه. |
| `billing.plan.pro.desc` | Remote play through the relay at full quality, {gb} GB a month on up to {streams} devices at once. After that, playback continues at Basic quality, never stops. | التشغيل عن بُعد عبر الوسيط بالجودة الكاملة، {gb} غيغابايت شهريًا على ما يصل إلى {streams} أجهزة في الوقت نفسه. بعدها يستمر التشغيل بجودة الخطة الأساسية ولا يتوقف. |
| `billing.plan.basic.fairUse` | Fair use: after 500 GB in a month, relayed streams play at lower quality until the month ends. They never stop. | الاستخدام العادل: بعد 500 غيغابايت في الشهر، تُشغَّل البثوث عبر الوسيط بجودة أقل حتى نهاية الشهر، ولا تتوقف أبدًا. |
| `billing.plan.priceTbd` | Price coming soon | السعر قريبًا |
| `billing.payg.title` | Pay as you go | الدفع حسب الاستخدام |
| `billing.payg.desc` | Keep full quality after your monthly allowance, at {price} per GB, up to a monthly limit you set. Off unless you turn it on. | حافظ على الجودة الكاملة بعد رصيدك الشهري، بسعر {price} لكل غيغابايت، حتى حد شهري تحدده أنت. متوقف ما لم تفعّله. |
| `billing.payg.cap` | Monthly spending limit | حد الإنفاق الشهري |
| `billing.payg.capRequired` | Set a monthly spending limit to turn on pay as you go. | حدّد حدًا شهريًا للإنفاق لتفعيل الدفع حسب الاستخدام. |
| `billing.payg.spent` | {spent} of {cap} spent this month | أُنفق {spent} من {cap} هذا الشهر |
| `payg.nearCap` | You've used 80% of your pay-as-you-go limit this month. | استخدمت 80% من حد الدفع حسب الاستخدام لهذا الشهر. |
| `payg.capReached` | You've reached your pay-as-you-go limit. Remote play continues at Basic quality until next month. | بلغت حد الدفع حسب الاستخدام. يستمر التشغيل عن بُعد بجودة الخطة الأساسية حتى الشهر القادم. |
| `billing.extraStreams.title` | Extra devices at once | أجهزة إضافية في الوقت نفسه |
| `billing.extraStreams.desc` | Add relayed streams beyond your plan's {streams}, at {price} each a month. | أضف بثوثًا عبر الوسيط فوق الـ {streams} المتاحة في خطتك، بسعر {price} لكل بث شهريًا. |
| `billing.usage.title` | Relay use this month | استخدام الوسيط هذا الشهر |
| `billing.usage.allowance` | {used} of {allowance} at full quality | {used} من {allowance} بالجودة الكاملة |
| `billing.usage.mode.full` | Full quality | جودة كاملة |
| `billing.usage.mode.capped` | Capped quality (about 3 Mbps) | جودة محدودة (نحو 3 ميغابت في الثانية) |
| `billing.usage.mode.cappedSlow` | Lower quality until the month ends (fair use) | جودة أقل حتى نهاية الشهر (الاستخدام العادل) |
| `relay.needsPlan` | Remote play through the relay needs a Basic or Pro plan. Direct access from the internet is free: turn it on in this home's settings. | التشغيل عن بُعد عبر الوسيط يتطلب الخطة الأساسية أو الاحترافية. الوصول المباشر من الإنترنت مجاني: فعّله من إعدادات هذا الجهاز الرئيسي. |
| `relay.streamLimit` | {count} devices are already playing through the relay, the most your plan allows. Stop one, or play at home or over a direct connection. | يوجد {count} أجهزة تشغّل عبر الوسيط، وهو الحد الأقصى في خطتك. أوقف أحدها، أو شغّل في المنزل أو عبر اتصال مباشر. |
| `relay.homeBusy` | Your home is too busy to prepare this video for remote play right now. Try again shortly. | جهازك الرئيسي مشغول جدًا لتجهيز هذا الفيديو للتشغيل عن بُعد الآن. حاول بعد قليل. |
| `relay.transcodeNote` | On the Basic plan, videos played through the relay are converted on this home to about 3 Mbps. This uses its processor or graphics chip while they play. | في الخطة الأساسية، تُحوَّل الفيديوهات المشغَّلة عبر الوسيط على هذا الجهاز الرئيسي إلى نحو 3 ميغابت في الثانية، وهذا يستخدم معالجه أو شريحة الرسوميات أثناء التشغيل. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/billing/plans.go`, `plans_test.go` | New catalog (§2); `TBD` checks. |
| `cloud/internal/billing/meter.go` | Per-mode counting; `Allow` returns a mode, never `ErrOverLimit`. |
| `cloud/internal/billing/modes.go` (new) | §3 mode function. |
| `cloud/internal/billing/streams.go` (new), `cloud/cmd/maktaba-cloud/role_worker.go` | `relay_streams` counting and the sweeper. |
| `cloud/internal/billing/usage_report.go` (new) | Hourly Stripe meter events, queue, clamp, reconciliation. |
| `cloud/internal/billing/stripe.go` | Meter events; subscription items 2 and 3. |
| `cloud/internal/handlers/billing/` (plans API and the Stripe webhook) | Plans API (new fields), PAYG on/off and cap, extra streams; webhook for items 2 and 3. |
| `cloud/internal/handlers/relay/proxy.go` | Modes: `402 relay_needs_plan`, cap header, pacing, `429 stream_limit`. |
| `cloud/internal/license/license.go` | `TierForPlan` identity; `home` read as `basic` within grace. |
| `cloud/internal/handlers/homes/homes.go` | No `relay` connection for Free homes. |
| `cloud/migrations/` (new, expand-only) | CHECKs widened; `pro`→`basic` then `family`→`pro`; `relay_streams`, `relay_usage_monthly`, `payg_settings`, `stripe_meter_outbox`. A later contract migration drops `family`. |
| `streaming/internal/` (HLS master playlist and transcode decision) | Honour `X-Maktaba-Relay-Cap-Kbps` on tunnel requests only. |
| `api/internal/` license reader | New tier names; `home` alias within grace. |
| `web/cloud/src/pages/Billing.tsx`, `web/cloud/src/i18n/en.json`, `ar.json` | Three plans, usage, PAYG and cap, extra streams; remove `homeTier.*` and `seats`. |
| `web/src/` remote-access settings, `web/src/i18n/en.json`, `ar.json` | Current mode, `relay.transcodeNote`. |
| Apple `MaktabaKit` (`Localizable.xcstrings`), Android `values/strings.xml`, `values-ar/strings.xml` | `relay.needsPlan`, `relay.streamLimit`, `relay.homeBusy`. |
| `cloud/docs/accounts-billing-admin.md`, `specs/architecture.md` §13 | New plans, modes, meter (architecture edit with this story's code PR). |

### Tests

- T01–T12 in `relay.yml`'s `test` job, the streaming service's Go tests and each app's string
  checks; Stripe only through the test-mode mock, never live.
- **CI only:** rehearsal scenario `relay-plans` (50.06).

### Rollout

1. Expand migration and the new catalog, with paid plans still off (launch gate).
2. Home release that honours the cap header and reads the new tier names.
3. Relay modes on in staging; then production with `relay.plans.enforce = true`.
4. The owner sets prices, Pro's allowance and N, and creates the Stripe prices and meter
   (README §12); the launch gate (T12) then allows paid plans on.
5. Contract migration (drop `family`) one release later.

### Rollback

- `relay.plans.enforce = false`: every linked paid home is forwarded in `full`, Free homes stay
  `refused`; counting continues.
- `relay.stream_limit.enabled = false`: stops refusing streams (counting continues).
- Pay-as-you-go: `billing.payg.enabled = false` stops new meter events; queued ones are still
  sent so nothing used goes unbilled.
- The data migration is reversible while the old names are still allowed (`basic`→`pro` after
  `pro`→`family`).

### Dependencies

50.04 (two relay processes during a deploy must agree on stream counts), 50.05 (plan cache,
`NOTIFY tier_changed`, metering through a blip). Needed by the launch gate and by 50.16 (which
keeps the counts right across the pool).
