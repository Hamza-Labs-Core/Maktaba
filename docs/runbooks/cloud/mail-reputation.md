# Runbook: account mail bounce or complaint rate nears Resend's limit

> Account mail delivery reputation (`docs/cloud-mail.md`, #1714).

## When

- `mail_bounce_rate_high` (**SEV2**): 30-day hard-bounce rate reached 3 % (nearing Resend's 4 % limit).
- `mail_complaint_rate_high` (**SEV2**): 30-day complaint rate reached 0.06 % (nearing Resend's 0.08 % limit).

## Check

1. Open the cloud log view ([mail delivery alerts, last 24 h](https://admin.maktaba.tv/logs?service=cloud.api&level=error&last=24h)) and check `mail_bounce_rate_high`, `mail_complaint_rate_high`, `mail_bounced`, and `mail_complained`.
2. On the cloud node, run `/opt/maktaba-relay/ops/cloud-ops.py mail-status` to inspect `mails.events` (`bounced`, `hard_bounced`, `complained`, `suppressed_addresses`, `bounce_rate`, `complaint_rate`, `alerts`).
3. Check the Resend dashboard for the affected sending domain (`maktaba.tv`) and verify DNS alignment (`DKIM`, `SPF`, `DMARC`).

## Do

1. Hard-bounced and complained recipient hashes are already suppressed automatically for future sign-ups and notices (password resets remain allowed).
2. If sign-up spam is driving bounces, confirm Cloudflare Turnstile is active (`TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET_KEY`) and review IP abuse blocks in the operator console.
3. If delivery must be paused immediately, remove `RESEND_API_KEY` from the target environment and redeploy (or revoke the key in Resend); sign-ups can be verified manually with `cloud-ops.py verify-email`.

## Done when

- `cloud-ops.py mail-status` shows no new bounces or complaints and rates are trending below the thresholds.

## Tell users

- Only if outbound mail delivery was paused: use [canary-down.md](canary-down.md)'s status update flow.
