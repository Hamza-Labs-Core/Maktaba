# Runbook: the API's Tunnel is down

> Epic 50 stories 50.12, 50.31.

## When

`tunnel_connections_low`, or `api_down` (**SEV2**) while the relay is up.

## Check

1. `systemctl status cloudflared` on the leader; Cloudflare's status page; the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines).

## Do

1. `sudo systemctl restart cloudflared` on the leader.
2. Cloudflare's Tunnel down for long: deploy `origin_mode = aop` (50.31 §3). **Never open the API's port without AOP** (authenticated origin pulls).

## Done when

`api_down` is quiet and `https://api.maktaba.tv/readyz` answers.

## Tell users

Sign-in down: [templates/investigating.md](templates/investigating.md) for **Sign-in and accounts** ([status-page.md](status-page.md)).
