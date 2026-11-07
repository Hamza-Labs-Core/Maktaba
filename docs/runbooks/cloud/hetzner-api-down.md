# Runbook: the Floating IP can't follow the leader

> Epic 50 stories 50.07, 50.10, 50.12. Proven in CI first: the cloud rehearsal's `runbook-dns-swap` (the fake Hetzner API answering 503).

## When

- `fip_assign_failed` (**SEV1**): the callback could not move the Floating IP.
- `fip_mismatch` (**SEV1**): the Floating IP is on the node that doesn't lead.

## Check

1. Hetzner's status page; the callback's log on the leader (`journalctl -u patroni | grep fip`), and the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines).
2. The token: `/etc/maktaba/hcloud-token` on the leader still works (`curl -fsS -H "Authorization: Bearer $(sudo cat /etc/maktaba/hcloud-token)" https://api.hetzner.cloud/v1/floating_ips`).

## Do

1. The callback retries every 30 s: if the API is only slow, wait 2 minutes.
2. **The API answers**: assign it from the Hetzner console (Floating IPs → assign to the leader).
3. **The API is down**: Actions → **Cloud DNS swap** with `to: leader`. `relay.maktaba.tv` and `*.relay.maktaba.tv` point at the leader's own address through the Cloudflare API, TTL 60 s (`deploy/cloud-ha/runbook/dns-swap.sh`). The API, the web app and the console need nothing: they reach the leader through the Tunnel (50.31).
4. **When the Hetzner API is back**: assign the Floating IP to the leader, then **Cloud DNS swap** with `to: floating`.

## Done when

The relay's names answer from the leader (the canary is green), and once Hetzner is back, they point at the Floating IP again and `fip_mismatch` is quiet.

## Tell users

[canary-down.md](canary-down.md)'s while remote play is down; resolvers take up to 5 minutes to follow (the records' normal TTL).
