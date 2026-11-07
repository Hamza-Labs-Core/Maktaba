# Runbook: a relay certificate is close to its expiry

> Epic 50 stories 50.02, 50.10, 50.12.

## When

- `cert_expiring` (**SEV3**): 14 days left.
- `cert_expiring_soon` (**SEV2**): 7 days left.

## Check

Each node renews its own wildcard about 30 days before expiry, so one close to expiry means renewal failed. On that node: `docker logs maktaba-caddy 2>&1 | grep -i -E 'obtain|renew|error'`, and the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines).

## Do

1. **DNS-01 refused**: the Cloudflare token (`/etc/maktaba/cloudflare.token`) is revoked or lost its zone permission. Make a new one (Zone → DNS → Edit on maktaba.tv only), put it there, `sudo systemctl restart maktaba-caddy`.
2. **Let's Encrypt refuses**: Caddy falls back to ZeroSSL by itself; check both CAs' status pages. Never ask for more than 5 duplicates of a name set in a week (the rate limit).
3. Renew by hand with the same configuration: `sudo systemctl restart maktaba-caddy` makes Caddy try at once.

## Done when

`echo | openssl s_client -connect <node address>:443 -servername x.relay.maktaba.tv 2>/dev/null | openssl x509 -noout -enddate` shows a date about 90 days out, on both nodes.

## Tell users

Nothing: no user sees it unless it expires.
