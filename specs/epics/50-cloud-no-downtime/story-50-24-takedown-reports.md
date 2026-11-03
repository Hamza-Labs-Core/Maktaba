# Story 50.24 — Takedown, reports and repeat infringers

> Epic 50 · Phase B · Depends on: 50.12 (incident handling), 50.23, 50.25 · Needed by: the
> broadcasting gate

> **Not legal advice. Confirm with a lawyer before launch** (README §14.9).

## Why

Maktaba Cloud is an **infrastructure and hosting provider**: it carries publishers' streams and
charges for infrastructure, not content (README §14.6). Hosting safe harbours (US DMCA § 512(c);
the EU DSA's hosting rules) protect a host that has certain things in place:

- a designated agent;
- notice and takedown;
- a repeat-infringer policy;
- acting on actual knowledge;
- publisher terms that put rights on the publisher (50.20 §0).

This story builds the first four. They are what that status requires, and they are cheap to run.

## Description

### 1. The designated agent and the copyright page

- The page `maktaba.tv/legal/copyright` (English and Arabic) gives:
  - who the designated agent is, with an email (`copyright@maktaba.tv`) and a postal address;
  - the notice form;
  - the counter-notice form;
  - the repeat-infringer policy;
  - what happens after a notice.
- The owner registers the agent with the US Copyright Office ($6, renewed every 3 years).
  Renewal reminders go into the admin calendar at 90, 30 and 7 days. The directory's own
  reminder emails also go to the agent's address.
- The page's wording is a template that counsel reviews (README §14.9). The code only renders
  it.

### 2. Notices

- **The form** has the § 512(c)(3) elements:
  - the claimant and their contact details;
  - the work;
  - the URL or URLs on `watch.maktaba.tv`, plus the programme and time if they know them;
  - a good-faith statement;
  - an accuracy statement under penalty of perjury;
  - a signature (typed name).

  It also has the DSA Art. 16 fields (an explanation, and the notifier's name and email). It uses
  Turnstile and is rate-limited. Emails to the agent are entered into the same queue by a service
  admin.
- **Triage.** Each notice gets a `notice_id` and lands in the **moderation queue** in the cloud
  admin (service admins only; the API enforces the role and has a role test).
- The queue resolves the URL and time to `publisher, channel, programme, item ref`. It uses the
  guide the home published (the programme at that time), because the cloud never holds library
  data.
- **Action on a valid notice** (target ≤ 24 h, ≤ 1 h for urgent illegal content):
  - The default scope is **the item**. The cloud sends the home a signed instruction over the
    cloud link: `takedown {notice_id, item_ref or source_ref, scope}`.
  - The home removes the item's publishable mark (or the source's, when the notice names a source
    or a whole channel of it).
  - The item's slot plays the slate, and the scheduler replaces it at the next top-up (50.23 §3).
  - If the item is airing now, the edge switches the channel to the slate within 60 s through a
    revocation entry of `kind = programme` (50.25). It lasts until the home confirms the takedown
    or the programme ends.
  - The **channel** or **publisher** scope (for flagrant cases) uses the kill switch (50.25).
- **Acting on actual knowledge.** A report (§4), an authority's order or an obvious case seen by
  a service admin can be acted on without a formal notice, with the same actions. Every action
  records its basis (`notice`, `report`, `authority`, `admin_knowledge`).
- **Statement of reasons** (DSA Art. 17). The publisher gets an email and a Broadcast page notice
  (English and Arabic) with:
  - what was disabled and why;
  - the notice's claimant (unless the notice asks for privacy and counsel's template allows it);
  - how to counter-notify or appeal.
- **Counter-notice** (§ 512(g)). The publisher files it from the Broadcast page with the required
  statements. The claimant is sent a copy. Unless the claimant tells the agent they have gone to
  court, the item's mark can be restored **10–14 business days** later. The queue shows the
  window, and a service admin restores it. Restoring re-enables the item on the home through the
  same signed channel.
- **Invalid or abusive notices.** A notice missing required elements is answered with what's
  missing. A notifier who sends repeated manifestly unfounded notices can be suspended from the
  form (Art. 23), and the queue counts them.

### 3. The repeat-infringer policy

- Each **valid notice that leads to a takedown** is a **strike** on the publisher. Strikes are
  stored in `broadcast_strikes (publisher_id, notice_id, at, expires_at = at + 12 months,
  withdrawn)`.
- A successful counter-notice, or a notice withdrawn by the claimant, withdraws its strike.
- **1 strike:** a notice to the publisher. **2 strikes:** a warning that the next ends
  broadcasting. **3 active strikes in 12 months:** broadcasting ends.
  - A `publisher` revocation entry with `terminate` (50.25) takes every channel off air.
  - Broadcaster is cancelled at the period's end, with no refund for the period.
  - Pro guest sharing is turned off.
  - Remote play is untouched: it isn't broadcasting.
- An appeal is by email to the agent. A service admin can reinstate, and that is audited.
- The policy text is on the copyright page and is shown at publish time (50.22). The thresholds
  are settings (`broadcast.strikes.limit = 3`, `broadcast.strikes.window_months = 12`), changed
  only by a PR.

### 4. Reports from viewers

- A **Report** button on every watch surface (50.27). The categories are:
  - copyright (a short version of the notice form, with a link to the full one);
  - illegal content;
  - abuse or harassment;
  - other.
- A report carries the channel, the programme at that time (from the guide), the time, an
  optional email, and a free-text box. Turnstile protects it, and it is rate-limited per IP.
- Reports go to the moderation queue. A report about content (not about the publisher) is also
  shown to the publisher on their Broadcast page, without the reporter's email.
- **Urgent categories** (illegal content) page the on-call service admin as **SEV2** through
  50.12's runbook `broadcast-urgent-report`. The target to a decision is ≤ 1 h.

### 5. Records

Every notice, report, action, statement, counter-notice and strike is kept in the cloud database
with who and when. Retention is `broadcast.records_retention_years` (default 3, **to confirm with
counsel**). A service admin can export them for a lawyer as CSV and JSON.

## Acceptance criteria

- **Given** a valid copyright notice naming a programme on channel C at 20:00,
  **when** a service admin accepts it,
  **then** the home gets a signed takedown for that item, its slot shows the slate, the publisher
  gets a statement of reasons in English and Arabic, and a strike is recorded.
- **Given** the named item is airing now,
  **when** the notice is accepted,
  **then** viewers see the slate within 60 s.
- **Given** a counter-notice and no word of a lawsuit,
  **when** 10 business days pass,
  **then** the queue offers restore, and restoring re-enables the item on the home.
- **Given** a publisher with 2 active strikes,
  **when** a third valid notice is accepted,
  **then** every channel of theirs goes off air within 60 s, Broadcaster is set to cancel, and
  the publisher is told.
- **Given** a strike 12 months old,
  **when** the daily job runs,
  **then** it no longer counts.
- **Given** an urgent illegal-content report,
  **when** it arrives,
  **then** the on-call admin is paged within 90 s.
- **Given** a home user, or an account that isn't a service admin,
  **when** they call any moderation endpoint,
  **then** the API answers `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | notice validation | each required element missing | a reply naming what's missing; no action |
| T02 | unit | resolve URL + time → programme | published guide fixtures | the right item ref; "unknown" when the guide has no slot |
| T03 | integration | cloud + home stack | accept a notice | signed takedown at the home; the mark removed; slate in the slot |
| T04 | integration | airing now | accept | a `programme` revocation entry; the edge plays the slate ≤ 60 s |
| T05 | unit | strikes | 1, 2, 3 within 12 months; one withdrawn; one expired | notice / warning / terminate; the withdrawn and expired don't count |
| T06 | integration | counter-notice | file it; wait 10 business days (fake clock) | restore offered; restore re-enables the item |
| T07 | integration | reports | each category; the urgent one | queue entries; the publisher sees the content report; urgent pages (fake Alertmanager) |
| T08 | role test | cloud API | non-service-admin calls | `403` |
| T09 | Arabic | the copyright page, forms, statements, emails | strings check | Arabic present |
| T10 | rehearsal (CI, 50.29 `broadcast-takedown`) | home stack + edge + cloud | notice → takedown → counter → restore; 3 strikes | the timings and states above |

## Edge cases

- **The notice names a channel URL with no time.** The admin must narrow it to a programme or a
  source, or use the channel scope (kill switch). Channel scope is for flagrant cases.
- **The home is offline when a takedown is sent.** The instruction queues on the cloud link. The
  edge already shows the slate through the `programme` entry if the item is airing. When the home
  returns it applies the takedown before its next schedule top-up.
- **The publisher deletes the item themselves** before the takedown arrives. The takedown applies
  to nothing, and the notice is still recorded with its strike, since the notice was valid.
- **The same item noticed by two claimants.** One strike per item per 30 days.
- **A notice about content that isn't on Maktaba** (wrong site). It is rejected with an
  explanation.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `copyright.title` | Copyright and takedown | حقوق النشر وطلبات الإزالة |
| `copyright.intro` | Maktaba Cloud hosts channels that its users publish from their own homes; it doesn't choose what they show. If you believe a channel shows your work without permission, send us a notice. | تستضيف سحابة مكتبة قنوات ينشرها مستخدموها من منازلهم، ولا تختار ما تعرضه. إن كنت ترى أن قناة تعرض عملك دون إذن، فأرسل إلينا إشعارًا. |
| `copyright.notice.submit` | Send notice | أرسل الإشعار |
| `copyright.statement.goodFaith` | I believe in good faith that this use is not authorised by the owner, its agent, or the law. | أعتقد بحسن نية أن هذا الاستخدام غير مصرح به من المالك أو وكيله أو القانون. |
| `copyright.statement.accuracy` | The information in this notice is accurate, and under penalty of perjury, I am the owner or authorised to act for the owner. | المعلومات الواردة في هذا الإشعار دقيقة، وأقرّ تحت طائلة عقوبة شهادة الزور بأنني المالك أو مخوّل بالتصرف نيابة عنه. |
| `broadcast.takedown.notice` | "{programme}" was taken off your channel after a copyright notice. You can respond with a counter-notice. | أُزيل "{programme}" من قناتك بعد إشعار بشأن حقوق النشر. يمكنك الرد بإشعار مضاد. |
| `broadcast.strikes.warning` | Your channels have {count} valid copyright notices in the last 12 months. At {limit}, broadcasting ends for this account. | تلقّت قنواتك {count} إشعارات صحيحة بشأن حقوق النشر خلال آخر 12 شهرًا. عند بلوغ {limit} ينتهي البث لهذا الحساب. |
| `broadcast.terminated` | Broadcasting has ended for this account under our repeat-infringer policy. Remote play is not affected. | انتهى البث لهذا الحساب وفق سياستنا بشأن المخالفات المتكررة. لا يتأثر التشغيل عن بُعد. |
| `report.button` | Report | إبلاغ |
| `report.category.copyright` | Copyright | حقوق النشر |
| `report.category.illegal` | Illegal content | محتوى غير قانوني |
| `report.category.abuse` | Abuse or harassment | إساءة أو تحرش |
| `report.category.other` | Something else | أمر آخر |
| `report.thanks` | Thank you. We review every report. | شكرًا لك. نراجع كل بلاغ. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/moderation/` (new): `notices.go`, `strikes.go`, `reports.go`, `actions.go`, `records.go`, tests | Queue, validation, actions, strikes, records. |
| `cloud/internal/handlers/moderation/` (new) | Public forms (Turnstile), admin queue API (service admin only). |
| `cloud/internal/cloudlink/` | The signed `takedown` and `restore` instructions to homes. |
| `api/internal/broadcast/takedown.go` (new) | Apply and restore takedowns on the home; acknowledge. |
| `cloud/migrations/` (new) | `broadcast_notices`, `broadcast_actions`, `broadcast_strikes`, `broadcast_reports`, `broadcast_counter_notices`. |
| `web/cloud/src/pages/admin/Moderation*.tsx`, `web/cloud/src/i18n/en.json`, `ar.json` | The queue. |
| `web/packages/broadcast-edge/site/legal/` | Copyright page, forms (EN/AR). |
| `docs/runbooks/broadcast-urgent-report.md`, `docs/runbooks/broadcast-takedown.md` (50.12's runbook set) | Runbooks. |

### Tests

- T01–T09 in `relay.yml`'s `test` job (cloud Go), the API job, and the web and edge unit jobs.
- **CI only:** rehearsal `broadcast-takedown` (50.29).

### Rollout

1. The queue and forms ship dark.
2. The owner registers the agent.
3. Counsel reviews the page, the forms and the policy (README §14.9).
4. The page goes live before any `open` channel (the broadcasting gate, item 4).

### Rollback

The forms can be hidden behind a "write to the agent" email, while the queue stays. Actions and
strikes are data; a strike can be withdrawn by a service admin (audited).

### Dependencies

50.12 (runbooks, paging, severities). 50.23 (marks on the home). 50.25 (the `programme`,
`channel` and `publisher` entries).
