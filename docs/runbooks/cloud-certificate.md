# Runbook: a relay certificate is close to its expiry

> Alerts `cert_expiring` (14 days), `cert_expiring_soon` (7 days) (Epic 50 stories 50.02, 50.10).

Each node renews its own wildcard about 30 days before expiry. One close to expiry means renewal
failed: `docker logs maktaba-caddy | grep -i -E 'obtain|renew|error'` on that node.
- **DNS-01 refused**: the Cloudflare token (`/etc/maktaba/cloudflare.token`) is revoked or lost
  its zone permission; make a new one (Zone → DNS → Edit on maktaba.tv only), put it there,
  `systemctl restart maktaba-caddy`.
- **Let's Encrypt refuses**: Caddy falls back to ZeroSSL by itself; check both CAs' status pages.
  Never issue more than 5 duplicates of a name set in a week (the rate limit).
