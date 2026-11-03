# Story 50.11 — Status page and in-app status

> Epic 50 · Phase 1 · Depends on: 50.10 · Needed by: 50.12, 50.13

## Why

When remote play stops, a paying user's first question is "is it me or them?". Today nothing
answers it: no status page, nothing in the apps. Plex's July 2026 outage showed the other way
to get this wrong: its status page listed only guide data while people couldn't reach their
servers ([research.md](research.md) §2). Our page must come from the probe (50.10), not from
someone remembering to update it, and it must still load when Hetzner is down.

## Description

### 1. Where it lives

- `status.maktaba.tv`, a **static site** hosted **off Hetzner** on GitHub Pages (Cloudflare
  Pages is the fallback if Pages is ever down for us). DNS: a `CNAME` on Cloudflare, DNS only.
- Built from `status/` in this repo by a GitHub Actions workflow. Its data is plain files:
  - `status/data/checks/<component>/<yyyy-mm-dd>.json`: probe results per day, written by the
    outside prober (50.10) through a commit by its workflow, or pushed by the witness's
    Alertmanager webhook through a GitHub App with write access to `status/data/` only.
  - `status/data/incidents/<yyyy-mm-dd>-<slug>.md`: incident posts and maintenance notices,
    with front matter (`component`, `state`, `started`, `resolved`, `title_en`, `title_ar`) and
    one English and one Arabic section.
- The workflow rebuilds the site on every push to `status/` and every 5 minutes on a schedule,
  so a missed webhook still shows within 5 minutes.

### 2. Components and states

| Component | Fed by | Notes |
|---|---|---|
| Remote access (relay) | canary home probe `https://canary.relay.maktaba.tv/api/health` | The one paying users care about most. |
| Sign-in and accounts (API) | `https://api.maktaba.tv/readyz` | |
| Notifications | `push_outbox_lag_seconds` (> 300 s = degraded) | From the witness's Prometheus. |
| Payments | link to Stripe's own status page | We don't host it; we say where to look. |

States: **operational**, **degraded** (slow, or failing from one network only, or a held
request rate above 5 %), **outage** (both networks fail two probes in a row), **maintenance**
(a notice is active). The worst component sets the headline.

### 3. The page

- Headline, the four components, a **90-day uptime bar** per component (one cell a day,
  coloured by that day's uptime), current incidents, scheduled maintenance, and the past
  incidents of the last 90 days.
- **English and Arabic**: the language follows `Accept-Language` on first load, with a switch;
  Arabic pages are `dir="rtl"`. Times in UTC with the viewer's local time beside them.
- Works without JavaScript (the build writes the HTML); the switch and local time are
  JavaScript extras.
- **Maintenance** is posted at least **48 h** ahead (50.12 runbook). The page and the apps show
  it from then until it ends.

### 4. `summary.json` for the apps

`https://status.maktaba.tv/summary.json`:

```json
{ "updated_at": "2026-11-03T10:15:00Z",
  "overall": "degraded",
  "components": { "relay": "degraded", "api": "operational", "push": "operational" },
  "incident": { "id": "2026-11-03-relay-slow", "state": "identified",
                "title": { "en": "...", "ar": "..." }, "url": "https://status.maktaba.tv/i/2026-11-03-relay-slow" },
  "maintenance": [ { "starts_at": "...", "ends_at": "...", "components": ["relay"],
                     "title": { "en": "...", "ar": "..." } } ] }
```

- `Access-Control-Allow-Origin: *`, `Cache-Control: max-age=60`.
- Apps fetch it when they start, every 5 minutes while open, and at once when a relay request
  fails. They never block on it: a failed fetch shows nothing.

### 5. The banner in the apps

Web (the home's own web app when reached through the relay), cloud web (`app.maktaba.tv`),
Apple (Player, Remote, tvOS) and Android (Player, Remote, TV) show a slim banner when the
**relay** or **API** component is degraded, in outage, or under maintenance (now or within
24 h). It links to the status page and can be closed for that incident. It never shows for
`push` alone, and never inside the player while playing (it shows on pause or exit).

## Acceptance criteria

- **Given** the canary probe fails from both networks twice in a row,
  **when** 5 minutes have passed at most,
  **then** `status.maktaba.tv` shows Remote access as **outage**, and `summary.json` says
  `"relay": "outage"`.
- **Given** Hetzner is unreachable from everywhere,
  **when** someone opens `status.maktaba.tv`,
  **then** the page loads (it is on GitHub Pages) and shows the outage.
- **Given** a maintenance notice is committed 48 h ahead,
  **when** an app starts within those 48 h,
  **then** it shows the maintenance banner from 24 h before the start until the end, in the
  app's language.
- **Given** a browser set to Arabic,
  **when** it opens the page,
  **then** the page is in Arabic, right to left, with every component, state and incident title
  in Arabic.
- **Given** `summary.json` can't be fetched,
  **when** an app starts,
  **then** it shows no banner and no error, and plays as usual.
- **Given** a component recovers,
  **when** two probes in a row pass,
  **then** the state goes back to operational and the banner disappears on the next fetch.
- **Given** an incident file has an English title and no Arabic one,
  **when** the status workflow builds,
  **then** the build fails.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | status builder, fixture check files | build | uptime bars match the fixture's per-day uptime |
| T02 | unit | worst-component rule | relay degraded, api outage | overall = outage |
| T03 | unit | incident without `title_ar` | build | fails with the file name |
| T04 | unit | Arabic build | render | `dir="rtl"`, no English left in the template strings |
| T05 | unit (web, cloud web) | mocked `summary.json` per state | render shell | banner text and link per state; none for `push` only |
| T06 | unit (Apple, `MaktabaKitTests`) | as T05 | `StatusBanner` model | same states; dismissed per incident id |
| T07 | unit (Android, Robolectric) | as T05 | banner composable | same |
| T08 | unit (all apps) | `summary.json` 404 / timeout | start | no banner, no error |
| T09 | Arabic | every new key | strings check | each English key has Arabic (web `ar.json`, Android `values-ar`, Apple catalog) |
| T10 | rehearsal (CI, 50.06 `status-feed`) | prober + fake GitHub | kill the relay container | summary shows outage within 2 probe rounds |

## Edge cases

- **GitHub Pages down too.** The banner in the apps is driven by `summary.json`; if that's
  gone the apps show nothing, which is the same as today. The owner can switch the `CNAME` to
  Cloudflare Pages (runbook in 50.12).
- **Flapping.** A component must pass or fail two probes in a row to change state, so one lost
  packet doesn't flash a banner.
- **One network fails, the other passes.** Degraded, not outage: it is probably the probe's
  network.
- **Clock in incident files.** Times are UTC in ISO 8601; the build refuses anything else.
- **Privacy.** The page shows no home, user or count; only component states.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `status.title` | Maktaba Cloud status | حالة سحابة مكتبة |
| `status.overall.operational` | All services are working | كل الخدمات تعمل |
| `status.overall.degraded` | Some services are slower than usual | بعض الخدمات أبطأ من المعتاد |
| `status.overall.outage` | Some services are down | بعض الخدمات متوقفة |
| `status.overall.maintenance` | Planned maintenance in progress | صيانة مجدولة جارية |
| `status.component.relay` | Remote access | الوصول عن بُعد |
| `status.component.api` | Sign-in and accounts | تسجيل الدخول والحسابات |
| `status.component.push` | Notifications | الإشعارات |
| `status.component.payments` | Payments (Stripe) | المدفوعات (Stripe) |
| `status.state.operational` | Working | يعمل |
| `status.state.degraded` | Slow | بطيء |
| `status.state.outage` | Down | متوقف |
| `status.state.maintenance` | Maintenance | صيانة |
| `status.uptime90` | Uptime, last 90 days: {percent} | مدة التشغيل في آخر 90 يومًا: {percent} |
| `status.pastIncidents` | Past incidents | حوادث سابقة |
| `status.noIncidents` | No incidents in the last 90 days. | لا حوادث في آخر 90 يومًا. |
| `status.payments.link` | Payments are handled by Stripe. See Stripe's status page. | تتولى Stripe المدفوعات. راجع صفحة حالة Stripe. |
| `banner.relay.degraded` | Remote access is slower than usual. Playback at home is not affected. | الوصول عن بُعد أبطأ من المعتاد. التشغيل داخل المنزل لا يتأثر. |
| `banner.relay.outage` | Remote access is down. We're working on it. Playback at home still works. | الوصول عن بُعد متوقف، ونعمل على إصلاحه. التشغيل داخل المنزل ما زال يعمل. |
| `banner.api.outage` | Signing in with Maktaba Cloud isn't working right now. You can sign in to your home with its password. | تسجيل الدخول عبر سحابة مكتبة لا يعمل الآن. يمكنك تسجيل الدخول إلى الجهاز الرئيسي بكلمة مروره. |
| `banner.maintenance` | Planned maintenance on {date}, {start}–{end}. Remote play may pause for a few seconds. | صيانة مجدولة في {date}، من {start} إلى {end}. قد يتوقف التشغيل عن بُعد بضع ثوانٍ. |
| `banner.details` | Details | التفاصيل |
| `banner.dismiss` | Dismiss | إخفاء |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `status/` (new) | Static site source: templates (EN/AR), `build.ts` (or Go `cmd`), `data/checks/`, `data/incidents/`, `CNAME`. |
| `.github/workflows/status-page.yml` (new) | Build and publish to GitHub Pages on push to `status/**` and every 5 min; check Arabic titles. |
| `.github/workflows/status-probe.yml` (new) | The outside prober from 50.10 writes `status/data/checks/` (one of its two networks is GitHub's runners). |
| `web/src/components/OfflineBanner.tsx` (or a new `CloudStatusBanner.tsx` beside it) | Banner from `summary.json` in the home's web app. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `banner.*` keys. |
| `web/cloud/src/ui.tsx`, `web/cloud/src/i18n/en.json`, `web/cloud/src/i18n/ar.json` | Banner in the cloud web app. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/CloudStatus.swift` (new), `Localizable.xcstrings` | `summary.json` fetch, banner view used by Player, Remote and tvOS. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/CloudStatus.kt` (new) | Fetch and model. |
| `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/CloudStatusBanner.kt` (new), `apps/android/core/src/main/res/values/strings.xml`, `values-ar/strings.xml` | Banner composable and strings. |
| `docs/runbooks/cloud/status-page.md` (new, with 50.12) | How to post an incident or maintenance notice. |

### Tests

- Unit tests T01–T09 in each app's normal test job; the Arabic checks join the existing
  missing-Arabic tests.
- UI tests for the banner only in CI (Apple and Android UI shards), never locally.
- **CI only:** rehearsal scenario `status-feed` (50.06).

### Rollout

1. Publish `status.maktaba.tv` with the components in "operational" and the prober running
   against the production pair (after 50.10).
2. Ship the app banners in the next release; they show nothing until the page reports a
   problem.
3. Launch gate item 4 (README §11) checks the page and banner are live.

### Rollback

- The apps treat a missing or broken `summary.json` as "no banner", so taking the page down
  removes every banner.
- A wrong state is fixed by committing a corrected incident file; the site rebuilds in a
  minute.

### Dependencies

50.10 (the probe that feeds it). Needed by 50.12 (incident posts) and 50.13 (apps use the
banner).
