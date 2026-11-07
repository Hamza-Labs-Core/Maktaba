# Runbook: a secret leaked, or might have

> Epic 50 story 50.12, Epic 25 (25.26).

## When

A secret was exposed (a log, a screenshot, a lost laptop, a former helper), or a scan says so. **SEV1.**

## Check

1. Which secrets, and since when. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): anything done with them (the staff and admin audit rows, the Hetzner and Cloudflare audit logs).

## Do

In this order (the cheapest to rotate first, the one that needs a release last):

1. **Hetzner token**: make a new one in the console, put it in `/etc/maktaba/hcloud-token` on both nodes and W, revoke the old.
2. **Cloudflare token(s)**: the DNS-edit token (`/etc/maktaba/cloudflare.token`, the `CF_DNS_TOKEN` secret), the Tunnel's; revoke the old.
3. **Payment provider keys** and the webhook signing secret.
4. **The token secret** (`MAKTABA_CLOUD_TOKEN_SECRET`): homes fall back to their bearer and reconnect; resume tickets die (a reconnect costs one password check each).
5. **The entitlement key, last**: it needs a coordinated release (25.26): homes must trust the new key before the old one stops signing.
6. Any GitHub secret it touched (`RELAY_SSH_KEY`, `PUSHGATEWAY_TOKEN`): replace in Settings → Secrets.

## Done when

Every exposed secret is replaced and the old one revoked, and nothing in the audit shows its use after the leak.

## Tell users

Only if user data was at risk: an email to the affected accounts, plainly, within 72 hours. A postmortem within 5 days.
