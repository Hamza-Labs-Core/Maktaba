# Runbook: a traffic spike or abuse

> Epic 50 story 50.12, Epic 25 (25.20).

## When

A traffic spike on the relay or the API, an abuse-score alert, or a report.

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the saved search `top homes by bytes, last hour` and `requests by source, last 10 min`.
2. Is it one home (a viral link, a scraper) or many sources (an attack)?

## Do

1. **One home**: suspend it from the service admin view (25.20); the owner is told by email.
2. **The API**: turn on Cloudflare's "Under attack" mode for `api.maktaba.tv` (it is proxied).
3. **The relay** (DNS only, not behind Cloudflare's proxy): Hetzner Cloud Firewall rules by source address on both nodes; keep 22 open to the owner's address.

## Done when

Traffic is back to its usual level and the canary is green; undo "Under attack" and temporary firewall rules after a day.

## Tell users

[canary-down.md](canary-down.md)'s if remote play suffered.
