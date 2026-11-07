---
schema: 1
kind: pr
pr: 873
title: Cloudflare in front of the cloud (Epic 50, 50.30)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [867]
---

## What happened
- The zone's TLS settings, WAF managed rules, four custom rules and two rate limits became a checked file, `deploy/cloudflare/zone.maktaba.tv.json`; `tools/cloudflare/zone.mjs` checks it offline against the plan and plans or applies it from #867's workflow, reading back.
- The api gained an optional Turnstile check on register, login and password/forgot from the web app (`cloud/internal/turnstile`), off by default; the web app shows the widget and explains the new errors in English and Arabic.

## What we learned
- The cloud's config parser ignores unknown keys, so a secret written in the file under a plausible key would be silently dropped: refuse such a key by name instead of letting it pass.
- `MAKTABA_ENV` defaults to prod, so a new production-only requirement must be off by default and turned on in the nodes' config, or dev and CI break.
- The owner's secret names (the GitHub environment's) and the binary's environment variables differ; the go-live list must name both and where each lives (`/etc/maktaba/cloud.env` on the nodes).

## Keep doing
- Map new API errors through the web app's existing `error.<code>` keys, even when a spec names others; say so in the pull request.

## Open
- Whether Pro accepts `log` as a rate-limit action is unconfirmed; the owner's first apply shows it (the fallback is `managed_challenge` for the observation week).
- 50.32's CSP must allow `https://challenges.cloudflare.com` for the widget.
