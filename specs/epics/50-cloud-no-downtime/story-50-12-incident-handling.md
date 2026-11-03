# Story 50.12 — Incident handling

> Epic 50 · Phase 1 · Depends on: 50.10, 50.11 · Needed by: launch gate (README §11)

## Why

Failover and alerts handle the failures we planned for. The rest need a person, and today
that person is the owner, alone, maybe at 3 a.m. The rule is that recovery must be
automatic or one click, never a terminal, so it still works once many people use the service.
Where a person must act, they need a short, tested page that says what to do, and
users need to hear what is going on in their language.

## Description

### 1. Severities

| Level | Means | Examples | Response | Update users |
|---|---|---|---|---|
| **SEV1** | Paying users can't play remotely, or data is at risk | both nodes down; relay down > 3 min; database lost; secrets leaked | acknowledge in **15 min**, any hour | status page within **15 min**, then every 30 min |
| **SEV2** | Degraded, or one safety net gone | standby down (no failover possible); backups failing > 24 h; sign-in down but relay up; failover happened (check it) | acknowledge in **1 h** (07:00–24:00 local), else next morning | status page if users notice |
| **SEV3** | No user impact yet | certificate expires in < 14 days; disk 80 %; cost past 80 % of ceiling | next working day | no |

The owner is on call alone. Alerts (50.10) carry the severity; SEV1 pages by phone push and
repeats every 5 minutes until acknowledged; SEV2 pushes once; SEV3 is an email digest.

### 2. Runbooks

Each runbook lives in `docs/runbooks/cloud/` (new), one file each, same layout: **When**
(the alert that links here), **Check** (read-only: first a saved search in the cloud log viewer
and the error groups of [Epic 53.17](../53-logs-errors-reports/story-53-17-cloud-observability.md),
which keep a dead node's last lines on the witness; then commands), **Do** (numbered steps, each
either a one-click GitHub Actions workflow or one command), **Done when**, **Tell users**
(which template). Every alert in 50.10 links to one.

| Runbook | When | The action |
|---|---|---|
| `failover-check.md` | automatic failover happened | Confirm the new leader serves, the old node is fenced, replication restarts when it is back; open a postmortem. |
| `switchover.md` | planned node work | Post maintenance 48 h ahead; Actions → **Cloud switchover** (Patroni switchover, Floating IP follows); patch; switch back if wanted. |
| `forced-failover.md` | leader is sick but alive (stuck disk, OOM loop) | Actions → **Cloud failover** (fence the leader, promote the standby). |
| `hetzner-api-down.md` | the Floating IP can't move | Actions → **Cloud DNS swap**: point `relay.maktaba.tv`, `*.relay.maktaba.tv` and the API origin at the standby's primary IP through the Cloudflare API (TTL 60 s). Swap back when the API returns. |
| `restore.md` | both nodes lost, or the zone | Order a node in another zone (or provider), run `restore.sh` from 50.09, DNS swap, tell users. Target: 4 h. |
| `deploy-rollback.md` | a deploy is bad after it passed health checks | Actions → **Relay deploy** with the previous tag (blue-green, 50.08). Never roll the schema back: migrations are expand-only (50.08). |
| `cert-expiry.md` | certificate < 14 days | Check DNS-01 renewal logs on both nodes; renew by hand with the same command; switch to the ZeroSSL issuer if Let's Encrypt is down. |
| `payments-backlog.md` | webhook failures or lag | The payment provider (TBD, not Stripe) retries its webhooks: fix the cause, then replay from the provider's dashboard; run the daily reconciliation (25.14) at once. |
| `abuse-ddos.md` | traffic spike, abuse score alerts | Suspend the home (25.20); turn on Cloudflare "Under attack" for `api.maktaba.tv`; for the relay, Hetzner Cloud Firewall rules by source. |
| `secrets-rotation.md` | a secret leaked or might have | Order: Hetzner token, Cloudflare token, payment provider keys, token secret (homes fall back to bearer; tickets die), entitlement key last (needs a coordinated release; 25.26). |
| `status-page.md` | posting an incident or maintenance | Add a file under `status/data/incidents/` from the templates below; commit. |

The forced-failover, switchover, DNS-swap and deploy workflows are proven in the cloud
rehearsal (50.06), never first on the VPS (project rule: update and recovery paths are proven in
CI first).

### 3. Telling users

Templates in `docs/runbooks/cloud/templates/` (new), English and Arabic in one file each, for
the four incident stages plus maintenance (the strings below). They go on the status page
(50.11), which feeds the in-app banner. For SEV1 longer than 1 hour, the owner also emails
paying users from the cloud's mailer (25.2's Postmark), using the same text.

### 4. Postmortems

Every SEV1 and every automatic failover gets a postmortem within **5 days**, from
`docs/runbooks/cloud/postmortem-template.md` (new): summary, impact (minutes, homes, error
budget used), timeline (UTC), cause, what went well, what didn't, actions with owners and
issue links, and the cloud log searches and error groups (Epic 53.17) that show the cause.
Blameless. A summary goes on the incident's status page entry, and the Claude
trail gets a line (in a trail-only PR).

### 5. No downtime credits (decided 2026-11-03)

The **99.9 % uptime target stays** (README §4.2): it drives alerts, error budgets and
postmortems, and the status page shows the measured figure. But it is a **target, not a
promise**: Maktaba Cloud gives **no credit, refund or discount** for downtime (README §13,
decision 1). No credit job, no payment provider balance entries, no credit setting. The status page
(50.11) and the Billing page state the target and say plainly that there is no credit (the
`uptime.policy` string below); no page, email or template may offer one.

## Acceptance criteria

- **Given** any alert defined in 50.10,
  **when** it fires,
  **then** its message carries a severity and a link to an existing file in
  `docs/runbooks/cloud/`.
- **Given** a SEV1 alert,
  **when** 5 minutes pass without acknowledgement,
  **then** it pages again, until acknowledged.
- **Given** a runbook names a GitHub Actions workflow,
  **when** the cloud rehearsal runs,
  **then** that workflow's steps run against the rehearsal pair and pass.
- **Given** an incident file built from a template,
  **when** it is committed,
  **then** the status page shows it in English and Arabic within 5 minutes.
- **Given** a month at 99.7 % measured uptime,
  **when** the month ends,
  **then** the status page shows 99.7 % against the 99.9 % target, a postmortem is due, and
  no credit, refund or balance entry is made for any account.
- **Given** a SEV1 resolved,
  **when** 5 days pass without a postmortem file,
  **then** a SEV3 reminder is sent.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | lint (CI) | alert rules (50.10) | parse | every rule has `severity` and a `runbook` URL that exists in the repo |
| T02 | lint (CI) | runbooks | parse | each has When / Check / Do / Done when / Tell users |
| T03 | lint (CI) | templates | parse | every template has English and Arabic |
| T04 | rehearsal (CI, 50.06 `runbook-switchover`) | rehearsal pair | run the switchover workflow | leader moves; canary pause ≤ 15 s |
| T05 | rehearsal (CI, 50.06 `runbook-dns-swap`) | fake Hetzner API returning 503, fake Cloudflare | run DNS swap | records point at the standby; canary recovers |
| T06 | rehearsal (CI, 50.06 `runbook-forced-failover`) | rehearsal pair | run forced failover | old leader fenced, standby serves |
| T07 | lint (CI) | templates, status page strings, `web/cloud` i18n | search for credit / refund wording | none offers a credit; `uptime.policy` exists in English and Arabic |

## Edge cases

- **The owner is away.** Automatic failover covers one node; for a SEV1 the owner can't reach,
  the status page says so honestly within the template ("we're investigating"), driven by the
  probe. A second responder is a later decision.
- **Alerts from a broken witness.** The outside probes (50.10) page directly, not through the
  witness, so losing the witness doesn't silence alerts.
- **A user asks for a credit after an outage.** The answer is the published policy
  (`uptime.policy`): a target, no credit. Support may still help case by case outside the
  product; the product never automates one.
- **Planned maintenance.** Counted against the uptime target like any downtime (the README
  counts planned work against the SLO).
- **Postmortem leaks.** Status page summaries never name a user, home or IP.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `incident.investigating` | We're looking into a problem with {component}. Playback at home is not affected. Next update within 30 minutes. | نتحقق من مشكلة في {component}. التشغيل داخل المنزل لا يتأثر. التحديث التالي خلال 30 دقيقة. |
| `incident.identified` | We found the cause of the problem with {component} and are fixing it. | وجدنا سبب المشكلة في {component} ونعمل على إصلاحها. |
| `incident.monitoring` | A fix is in place. We're watching {component} to make sure it stays working. | طُبّق الإصلاح، ونراقب {component} للتأكد من استمرار عمله. |
| `incident.resolved` | {component} is working again. It was affected from {start} to {end}. We'll post what happened within 5 days. | عاد {component} إلى العمل. تأثر من {start} إلى {end}. سننشر ما حدث خلال 5 أيام. |
| `incident.maintenance` | Planned maintenance on {date}, {start}–{end}. Remote play may pause for a few seconds. Playback at home is not affected. | صيانة مجدولة في {date}، من {start} إلى {end}. قد يتوقف التشغيل عن بُعد بضع ثوانٍ. التشغيل داخل المنزل لا يتأثر. |
| `incident.email.subject` | Maktaba Cloud: remote access problem | سحابة مكتبة: مشكلة في الوصول عن بُعد |
| `uptime.policy` | We aim to keep remote access available 99.9% of every month, and we publish how we did. This is a target, not a guarantee: we don't give credits or refunds for downtime. | نسعى لإبقاء الوصول عن بُعد متاحًا 99.9% من كل شهر، وننشر ما حققناه. هذا هدف وليس ضمانًا: لا نقدّم رصيدًا أو استردادًا عن فترات التوقف. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `docs/runbooks/cloud/` (new) | The eleven runbooks above, `templates/` (EN/AR), `postmortem-template.md`, `README.md` (severities, on-call). |
| `.github/workflows/cloud-switchover.yml`, `cloud-failover.yml`, `cloud-dns-swap.yml` (new) | One-click actions, `production` environment, required reviewer = owner. |
| `deploy/cloud-ha/alerts/*.yml` (from 50.10) | `severity` and `runbook` labels on every rule. |
| `tools/runbooks/lint.py` (new) | T01–T03; runs in CI on changes to `docs/runbooks/**` or the alert rules. |
| `web/cloud/src/pages/` (Billing page), `web/cloud/src/i18n/en.json`, `ar.json` | Show the uptime target and `uptime.policy` (no credit). |

### Tests

- T01–T03 and T07 lint in CI.
- **CI only:** rehearsal scenarios `runbook-switchover`, `runbook-dns-swap`,
  `runbook-forced-failover` (50.06), using fake Hetzner and Cloudflare APIs.

### Rollout

1. Runbooks and templates first (docs only), then the one-click workflows once 50.07 exists.
2. A game day before launch: the owner runs `switchover.md` and `forced-failover.md` on the
   production pair (launch gate item 2).

### Rollback

- Workflows are additive; delete or disable one to remove it.

### Dependencies

50.10 (alerts to link) and 50.11 (where users read updates).
