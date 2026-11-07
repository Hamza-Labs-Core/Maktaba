# Runbook: the Floating IP is not on the leader

> Alerts `fip_assign_failed`, `fip_mismatch` (Epic 50 stories 50.07, 50.10).

1. The callback keeps retrying every 30 s: check Hetzner's status page and the API token
   (`/etc/maktaba/hcloud-token` on the leader).
2. Move it by hand from the Hetzner console (Floating IPs → assign to the leader), or with the API:
   `curl -X POST -H "Authorization: Bearer $(cat /etc/maktaba/hcloud-token)" -H 'Content-Type: application/json' -d '{"server": <the leader's server id>}' https://api.hetzner.cloud/v1/floating_ips/<id>/actions/assign`.
3. **The Hetzner API is down**: point the relay names (`relay`, `*.relay`, docs/cloud-dns.md) at
   the leader's own IPv4 in Cloudflare, TTL 60 s. Point them back at the Floating IP once it
   follows the leader again.
