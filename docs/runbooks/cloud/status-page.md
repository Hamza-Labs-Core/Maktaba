# Runbook: the status page (status.maktaba.tv)

> Epic 50 story 50.11. The page is built from [`status/`](../../../status/) and the `status-data`
> branch, published on GitHub Pages (off Hetzner) by
> [`status-page.yml`](../../../.github/workflows/status-page.yml). Component states come from the
> probe, never by hand; people write only the posts.

## When

Posting an incident (a SEV1 within 15 minutes, a SEV2 when users notice) or a maintenance
notice (48 hours ahead).

## Check

The page and its probe: `https://status.maktaba.tv` shows the state the alerts say; the latest
**Cloud probe** run in Actions is green.

## Do

Start from a template in [templates/](templates/): copy it to
`status/data/incidents/<yyyy-mm-dd>-<slug>.md`, fill in the times and the component.

### Post an incident

Once an outage has lasted 5 minutes (`docs/runbooks/cloud/canary-down.md`, step 6), add
`status/data/incidents/<yyyy-mm-dd>-<slug>.md` in a pull request and merge it. The page
rebuilds within a minute or two of the merge.

```markdown
---
component: relay
state: investigating
started: 2026-11-03T10:05:00Z
title_en: Remote access is down
title_ar: الوصول عن بُعد متوقف
---

## English

Remote access through Maktaba Cloud is down. Playback at home is not affected.

## العربية

الوصول عن بُعد عبر سحابة مكتبة متوقف. التشغيل داخل المنزل لا يتأثر.
```

- `component`: one or more of `relay`, `api`, `push`, `payments`, `plugins`, separated by commas.
- `state`: `investigating`, `identified`, `monitoring`, `resolved`. Update it in a new commit
  as you learn more; add a `### 10:40 UTC` heading and a line under both sections for each update.
- `resolved` (the time) is required once `state: resolved`.
- `impact: degraded` or `impact: outage` sets the component's state while the incident is
  open. Use it only for what the probe can't see (payments, a partial failure); the probe
  already sets relay, API and notifications.
- Times are UTC in ISO 8601 (`2026-11-03T10:05:00Z`). Anything else fails the build.
- Both titles and both sections are required; a missing one fails the build with the file's
  name (the pull request's `Status page` check shows it before the merge).
- Write plainly; never offer a credit or refund: the uptime policy is a target (`uptime.policy`).

### Post a maintenance notice

At least **48 hours** ahead. The apps show it from 24 hours before the start until the end;
the page shows it from the merge until the end.

```markdown
---
kind: maintenance
component: relay, api
state: scheduled
starts: 2026-11-05T02:00:00Z
ends: 2026-11-05T02:30:00Z
title_en: Database upgrade
title_ar: ترقية قاعدة البيانات
---

## English

We're upgrading the database. Remote play may pause for a few seconds.

## العربية

نرقّي قاعدة البيانات. قد يتوقف التشغيل عن بُعد بضع ثوانٍ.
```

During the window the components read **maintenance**; after `ends` the notice moves to the
past. Set `state: completed` if it ends early; to cancel, delete the file.

### Fix a wrong state

The state follows the probe: two rounds in a row (30 s apart) change it. If the page is wrong,
check the probe first (`Cloud probe` runs in Actions; the witness's
`systemctl status maktaba-status-export.timer`). A wrong post is fixed by another commit.

### When GitHub Pages is down

The apps read `summary.json`; with the page gone they show nothing, which is safe. To move the
page to Cloudflare Pages: build it (`node status/build.mjs --checks <status-data checkout>
--out site`), deploy `site/` to a Cloudflare Pages project (it reads `_headers`), and point
the `status` CNAME at the project (DNS only). Move it back the same way.

### Setup (once, by the owner)

1. Settings → Pages: Source **GitHub Actions**; custom domain `status.maktaba.tv`; enforce HTTPS.
2. Cloudflare DNS: `status` CNAME `hamza-labs-core.github.io`, DNS only.
3. Cloudflare DNS: `witness` A record to the witness's IPv4, DNS only; `WITNESS_DOMAIN` in the
   witness's `cloud-ha.env`.
4. Repository secrets: `PUSHGATEWAY_URL` = `https://witness.maktaba.tv`, `PUSHGATEWAY_TOKEN` =
   the witness's `/etc/maktaba/witness.token`.
5. Repository variables: `CLOUD_PROBE` = `true` (the probe) and `STATUS_PAGE` = `true` (the page
   and the `status-data` branch).

## Done when

The page shows the post in English and Arabic (within 5 minutes of the merge), and the apps'
banner follows it.

## Tell users

This is how users are told.
