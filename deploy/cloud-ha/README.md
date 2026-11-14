# Maktaba Cloud: the Phase 1 pair (no downtime)

Two machines that cover for each other, and a witness that settles who is in charge (Epic 50,
story 50.07). The single-VPS setup in [`deploy/cloud-relay/`](../cloud-relay/) stays for
development and a first demo; production runs this.

```
   relay.maktaba.tv, *.relay.maktaba.tv ──► Floating IP ──► the leader
        ┌──────── node A (FSN1) ────────┐   ┌──────── node B (NBG1) ────────┐
        │ Caddy (its own wildcard)       │   │ Caddy (its own wildcard)       │
        │ the cloud processes (if leader)│   │ (stopped while standby)        │
        │ Postgres 16 + Patroni ─ sync ──┼──►│ Postgres 16 + Patroni          │
        │ etcd · Redis (cache only)      │   │ etcd · Redis (cache only)      │
        └──────────────┬─────────────────┘   └──────────────┬─────────────────┘
                       └──── private network ──► witness W (HEL1): etcd ◄┘
```

**The rule: everything follows the database leader.** Patroni holds a 30 s leader lease in
etcd (three members, so two nodes can never both win). On the leader, Patroni's role-change
callback ([`bin/on-role-change`](bin/on-role-change)) moves the Floating IP to it (one Hetzner
API call, retried; `fip_assign_failed` pages after 60 s) and starts the cloud processes
(`maktaba-cloud-active.target`); on any other role it stops them. The leader guard
([`bin/leader-guard`](bin/leader-guard)) also stops them as soon as Patroni stops calling the
node the primary, without waiting for a demotion to finish. DNS never changes on failover.

Replication is synchronous with fallback (`synchronous_mode: true`, `strict: false`): a commit
returns once the standby has it (RPO 0 while both run), and a primary whose standby is gone
keeps taking writes alone.

## Proven in CI

The cloud rehearsal ([docs/cloud-rehearsal.md](../../docs/cloud-rehearsal.md)) runs this
directory's files on three containers (`tools/rehearsal-cloud/compose-ha.yml`), with a fake
Hetzner Floating IP API:

| Scenario | Measured locally |
|---|---|
| `failover-kill-leader` (300 homes, 50 streams, the leader killed) | the other node led after 38 s; every home back after 53 s; remote play down 47 s (budget 3 min); 0 of 243 commits lost |
| `failover-network-partition` (the leader cut from B and W, still running) | read-only after 19 s, its relay stopped after 20 s (budget 40 s); never two writable primaries; 0 commits lost; the IP on the new leader; the old one back as a streaming replica |
| `switchover` (`bin/switchover` under traffic) | done in 3.2 s; remote play paused 2 s (budget 15 s); 0 commits lost |

The scripts' own tests: [`tests/scripts-test.sh`](tests/scripts-test.sh) (the callback, the
guard, the switchover's checks).

## What the owner creates (nothing in the repo calls these APIs)

1. A Hetzner project **maktaba-prod** with only these resources.
2. A private network `10.50.0.0/16`, subnet eu-central.
3. Servers, Ubuntu 24.04, SSH key only: **A** in FSN1 and **B** in NBG1 (each at least 2 vCPU,
   4 GB; backups on), and the witness **W**, a CPX22, in HEL1.
4. One Floating IPv4 in eu-central, assigned to A.
5. A Hetzner Cloud Firewall on all three: public 22 from the owner's and the deploy runner's
   addresses, 80 and 443 on A and B, 53/udp and 53/tcp on A and B once the direct-name
   domains exist (the dns role, story 50.15; `DIRECT_DNS=yes`); everything else from the
   private network.
6. A project API token (read/write) in `/etc/maktaba/hcloud-token` on A and B (0600) and as
   the GitHub `production` secret `HCLOUD_TOKEN`.
7. The Cloudflare DNS-edit token (Zone → DNS → Edit on `maktaba.tv` only) in
   `/etc/maktaba/cloudflare.token` on A and B, and the records of
   [docs/cloud-dns.md](../../docs/cloud-dns.md) pointed at the Floating IP.
8. The etcd certificates, on the owner's machine:
   `deploy/cloud-ha/bin/make-etcd-certs ~/maktaba-etcd node-a=<A's private IP> node-b=<B's> witness=<W's>`;
   copy `ca.crt` and each member's pair to its `/etc/etcd/pki/`. Keep the CA key in the
   password manager.
9. `/etc/maktaba/cloud-ha.env` on each machine, from [`cloud-ha.env.example`](cloud-ha.env.example).

Then, in this order:

```bash
sudo deploy/cloud-ha/setup.sh --role witness   # on W
sudo deploy/cloud-ha/setup.sh --role a         # on A: bootstraps the cluster
sudo deploy/cloud-ha/setup.sh --role b         # on B: joins as a replica
```

Before the launch gate (Epic 50 README §11): one planned switchover and one forced failover
(power off the leader in the Hetzner console), each within its budget.

## The origin only through Cloudflare (story 50.31)

The api, the web app's `/v1` and the console reach the leader only through the Cloudflare
Tunnel. `maktaba-cloudflared.service` (cloudflared, pinned in [`versions.env`](versions.env) and
installed from Cloudflare's signed repository) is part of `maktaba-cloud-active.target`, so
Patroni's callback starts it with the cloud processes on the leader and stops it on the standby.
Its token is in `/etc/maktaba/tunnel.token` (root:cloudflared, 0640): `deploy.sh` writes it on
both nodes from the `production` secret `CLOUDFLARE_TUNNEL_TOKEN`, over SSH's stdin; without it
the unit is skipped. Caddy serves relay names only (it removes `CF-Connecting-IP` on them), and
nothing opens the api's ports.

`/etc/maktaba/cloud.toml` on A and B:

```toml
[server]
env = "production"
listen_addr = "127.0.0.1:8090"
admin_listen_addr = "127.0.0.1:8091"   # the operator console's own listener (story 50.32)
admin_host = "admin.maktaba.tv"        # the one name it answers
user_hosts = ["api.maktaba.tv", "app.maktaba.tv", "maktaba.tv"]
trusted_proxies = ["127.0.0.1/32", "::1/128"]
origin_mode = "tunnel"
```

The api refuses to start in production with a listener that is not loopback or private, with no
`trusted_proxies`, or with the old `trust_proxy_headers`; and anywhere with `admin_listen_addr`
but no `admin_host` (or the reverse). The Tunnel's ingress sends `admin.maktaba.tv` to 8091 and
the user names to 8090: each listener answers only its own names (404 otherwise, logged once a
minute), so an ingress rule pointed at the wrong port serves nothing. `/healthz`, `/readyz` and
`/metrics` answer on any name, for the node's own probes.

**The fallback**, only if the Tunnel can't be used for a while, by a deploy: put the CA of
Maktaba's AOP client certificate in `/etc/caddy/maktaba-aop-ca.pem` on both nodes, set
`ORIGIN_MODE=aop` in `/etc/maktaba/cloud-ha.env` and `origin_mode = "aop"` (with
`aop_ca_file = "/etc/caddy/maktaba-aop-ca.pem"`) in `cloud.toml`, rerun `setup.sh`, then merge
`"mode": "aop"` with the Floating IP in `deploy/cloudflare/tunnel.maktaba.tv.json`. Caddy then
serves the three names, each request checked twice: Cloudflare's client certificate and a peer in
Cloudflare's ranges (`deploy/cloudflare/ips-v4.txt`, `ips-v6.txt`). Never one check alone.

## Deploys with no downtime (story 50.08)

`deploy/cloud-ha/deploy.sh <tag>` (relay.yml's deploy job when the repository variable
`CLOUD_HA` is `true`, or a laptop): Patroni says which node leads; the **standby** fetches the
release from its signed image and smoke-tests it against its own replica (a bad release stops
here); the **leader** runs the migrations (expand only: `tools/lint/cloud-expand-only.sh`
refuses a contract change in a release) and [`bin/bluegreen`](bin/bluegreen): the new colour
starts beside the old one and must stay ready for 30 s, then the old colour gets SIGTERM, its
`/readyz` turns `503 draining` so the edge sends new work to the new colour, and it drains its
homes' tunnels to it (DRAIN, 50.03); a 10-minute watch rolls back on its own. The colour and the
tag are recorded on both nodes, so a failover starts the same release. Then
[`bin/dns-update`](bin/dns-update) puts the release under the dns role (50.15), and
[`bin/app-update`](bin/app-update) under the api and worker (`/usr/local/bin/maktaba-cloud`),
each on the standby first (it only installs: those units don't run there) and then on the
leader, whose running units restart; one that doesn't stay up for 10 s gets the old binary back
and stops the deploy. The api and worker are not in the blue/green yet (50.08 plans them in
it), so the leader's api is down for its restart, about a second.

The edge is never reloaded during a deploy: both colours are its upstreams, health-checked
every second, and readiness moves the traffic (a reload through Caddy's admin API cut streams
in flight in the rehearsal). The cloud rehearsal's `deploy-blue-green` (1,000 homes, 0 failed
requests) and `deploy-bad-release-rollback` prove it before any deploy.

GitHub `production` environment secrets for it: `RELAY_HOST_A`, `RELAY_HOST_B` (the nodes' SSH
addresses), `RELAY_SSH_USER`, `RELAY_SSH_KEY` (the `deploy` user's key, which works only for
that user).

The release symbol tables' token (story 53.16): `deploy.sh` writes the repository secret
`MAKTABA_SYMBOLS_TOKEN` (the one the release workflows upload with) to `/etc/maktaba/symbols.token`
on both nodes (root:maktaba, 0640, over SSH's stdin), and adds
`MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=/etc/maktaba/symbols.token` to `/etc/maktaba/cloud.env`; the
leader's api reads it when step 6 restarts it. Without the secret nothing changes and
`POST /v1/symbols` answers `503 symbols_off`. Adding the secret is the owner's one step
([docs/testflight.md](../../docs/testflight.md#crash-symbols-owner)).

The operator console's staff keys (story 50.33): `/etc/maktaba/cloud.env` on both nodes carries
`MAKTABA_CLOUD_STAFF_TOKEN_SECRET` (`openssl rand -base64 48`, never the customers'
`MAKTABA_CLOUD_TOKEN_SECRET`) and `MAKTABA_CLOUD_STAFF_SECRET_KEY` (`openssl rand -hex 32`, which
seals each staff TOTP secret: lose it and every staff account needs `staff reset-mfa`). The api
refuses to start with `admin_listen_addr` set and either missing. Both go in the encrypted
secrets backup (`bin/backup-secrets`). The optional network allow-list is `cloud.toml`'s
`[admin] ip_allowlist = ["198.51.100.0/24"]` (empty: off); changed by a config deploy, and on the
leader over SSH with an api restart if it locks the owner out.

## Backups and restore (story 50.09)

pgBackRest writes the WAL archive (at least every minute) and every backup to **two
repositories**, each encrypted with its own passphrase: Hetzner Object Storage in HEL1 (repo1)
and Cloudflare R2 (repo2, the copy off Hetzner). `maktaba-backup.timer` runs on both nodes daily
at 03:00 UTC; only the leader backs up (full on Sunday, differential otherwise), so it follows a
failover. 30 days of point-in-time restore. The witness restores the latest backup to 5 minutes
ago every Monday (`maktaba-restore-drill.timer`, from R2 on the first Monday of a month) and
checks it against the live replica; `backup_stale`, `wal_archive_failing` and
`restore_drill_failed` page (`monitoring/backup-rules.yml`, tested by promtool in relay.yml).
A whole-zone loss: [docs/runbooks/cloud/restore.md](../../docs/runbooks/cloud/restore.md).
The secrets: `bin/backup-secrets` (age, the owner's key) puts an encrypted copy in both
repositories; the password manager keeps the originals.

What the owner creates for it: an Object Storage bucket `maktaba-prod-pg` in **HEL1** and an R2
bucket `maktaba-prod-pg-r2`, each with an access key limited to it, and the two passphrases
(`openssl rand -base64 48`) in the password manager; they go in `cloud-ha.env` (`REPO1_*`,
`REPO2_*`).

## Monitoring (story 50.10)

The witness runs Prometheus (30 days), Alertmanager, Grafana, the blackbox exporter and a
Pushgateway as Docker guests at a lower CPU and IO weight than etcd ([`monitoring/`](monitoring/)).
It scrapes each node over the private network: the leading relay's `/metrics` and Caddy's
through the node's private `:9443` site, Patroni's `/metrics`, postgres_exporter and node_exporter
(with the textfile collector for the backup and the callback's alerts). It probes the
**canary home** (`maktaba-canary`, a real cloudlink on W, slug `canary`) through the relay and
the API every 30 s, checks each node's own certificate, and checks every minute that the
Floating IP is on the leader (`bin/fip-check`). **Network 2** is GitHub Actions
(`.github/workflows/cloud-probe.yml`, on when the repository variable `CLOUD_PROBE` is `true`):
outside Hetzner, it probes the same URLs every 30 s and pushes the results to the witness.
The alert rules are [`monitoring/alerts.yml`](monitoring/alerts.yml) and `backup-rules.yml`,
each with a runbook in `docs/runbooks/`; `monitoring/check.sh` (relay.yml) runs promtool,
amtool and the rules' unit tests. Pages go to the owner's phone by ntfy, repeated every 15 min;
warnings by email.

What the owner sets up for it: the canary account (`canary`, Pro, excluded from revenue) and
its claim on W (`/etc/maktaba/canary.secret`, `CANARY_SERVER_ID`); an ntfy topic and token
(`NTFY_URL`, `NTFY_TOKEN`) or a Telegram bot; SMTP for the warning email (`SMTP_HOST`,
`SMTP_USER`, `SMTP_PASSWORD`, `ALERT_EMAIL`, `ALERT_EMAIL_FROM`); a read-only Hetzner token for
the Floating IP check on W; the hosted probe of network 1 (UptimeRobot or Better Stack, the
owner's choice) on the same three URLs; and, for network 2, the GitHub secrets
`PUSHGATEWAY_URL` and `PUSHGATEWAY_TOKEN` and the variable `CLOUD_PROBE=true`.

## The status page (story 50.11)

`status.maktaba.tv` is a static site on GitHub Pages, off Hetzner, built from
[`status/`](../../status/) by `.github/workflows/status-page.yml`. Its states come from the
probe, never by hand: each of network 2's rounds, beside network 1's (the witness's own probes
and series, written every 30 s by `bin/status-export` and read through the witness's one
public site, `monitoring/Caddyfile.witness`, behind the token), goes to the `status-data`
branch, and a component changing state (two rounds in a row) starts the page's build at once.
Both networks failing is an outage, one alone is degraded; the witness unreachable leaves the
outside network to decide. The apps read `summary.json`. Posting an incident or a maintenance
notice: [`docs/runbooks/cloud/status-page.md`](../../docs/runbooks/cloud/status-page.md).

What the owner sets up for it: GitHub Pages with the source **GitHub Actions** and the custom
domain `status.maktaba.tv`; the DNS-only records `status` (CNAME `hamza-labs-core.github.io`)
and `witness` (A, the witness's IPv4); `WITNESS_DOMAIN` in `cloud-ha.env`; the secret
`PUSHGATEWAY_URL=https://witness.maktaba.tv` with `PUSHGATEWAY_TOKEN` from the witness's
`/etc/maktaba/witness.token`; and the variable `STATUS_PAGE=true`.

## Incidents (story 50.12)

Every alert carries a severity (`sev1`, `sev2`, `sev3`) and a runbook in
[`docs/runbooks/cloud/`](../../docs/runbooks/cloud/README.md); Alertmanager pages SEV1 every 5
minutes until acknowledged, SEV2 once, and emails SEV3 daily. Three one-click workflows run
[`runbook/`](runbook/): **Cloud switchover** (`switchover.sh`), **Cloud failover**
(`failover.sh`) and **Cloud DNS swap** (`dns-swap.sh`, when the Hetzner API is down). The cloud
rehearsal runs the same scripts first against its own pair.

What the owner sets up for it: a required reviewer (the owner) on the `production` environment;
the secrets the deploy already uses (`RELAY_HOST_A`, `RELAY_HOST_B`, `RELAY_SSH_USER`,
`RELAY_SSH_KEY`) and, for the DNS swap, `CF_DNS_TOKEN` (a Cloudflare token with Zone → DNS →
Edit on maktaba.tv only), `CF_ZONE_ID`, `FLOATING_IP`, `PUBLIC_IP_A` and `PUBLIC_IP_B` (the
nodes' own public IPv4 addresses).

## The api pool (story 50.17, Phase 2)

Phase 2 runs the api and the worker on several machines at once, so losing one is a non-event
and adding one is a configuration change. The code is the same on one node: everything below
works unchanged on the Phase 1 pair, and nothing here is deployed until the owner creates it.

```
Cloudflare Tunnel ── cloudflared on api-1 and api-2 ── LB11 (private address only)
                                                        ├── api-1  (api + worker)
                                                        ├── api-2  (api + worker)
                                                        └── api-3  (api + worker)
                                                                │ private network
                                                           Postgres (the Phase 1 pair, then 50.18)
```

- **The worker runs once.** Every node runs a worker; its jobs run only on the node holding the
  Postgres session lock `pg_try_advisory_lock(8472613)` (one above the migrator's), tried every
  15 s on a connection of its own. A node that goes loses its session and the lock, and another
  takes it within 15 s; a stopped worker hands it over at once. The holder renews its row in
  `cloud_leaders` every 15 s; a heartbeat older than 60 s (a hung process that keeps its
  connection) lets another node end that backend. `worker_leader{node}` on each worker's
  `/metrics`; `worker_no_leader` (sev2) when no node holds it for 2 minutes
  ([runbook](../../docs/runbooks/cloud/worker-no-leader.md)).
- **Rate limits.** The limits that guard against brute force (sign-in, password reset, the mail
  routes, claim redeem, the console's sign-in) live in Postgres (`cloud_rate_buckets`, one
  UPSERT a request), so three nodes don't triple them; the general per-IP limits stay per node.
  With the database away a node's own bucket decides.
- **Caches.** Each relay keeps one LISTEN connection (`cloud/internal/cache`): the triggers of
  migration 00500003 (and a writer's `cache.Notify` on `cloud_cache`, in its transaction) drop
  an entry on every node within a second; a reconnect expires every entry. The api keeps no
  copy of a plan or an account: any node answers from the database.
- **Stopping a node.** On SIGTERM the api answers `/readyz` 503 for `server.drain_delay` (12 s on
  the pool) while it keeps serving, so the balancer takes it out before the port closes, then
  finishes what is in flight within `shutdown_grace`.

**What the owner creates** (none of it is called from the repository):

1. **LB11** in the private network, its public interface off (50.31): an HTTP service on port
   8090 to the targets' private `:8090`, and a second on 8091 to `:8091` (the console, 50.32);
   health check `GET /readyz` every 5 s, timeout 3 s, 2 failures to remove and 2 passes to add
   back; algorithm least connections; no sticky sessions.
2. **Three api machines** in the private network, each with `/etc/maktaba/cloud-ha.env`
   (`NODE_NAME`, `NODE_ADDR`, `PRIVATE_NET`, `SSH_FROM`; `TUNNEL=yes` on two of them),
   `/etc/maktaba/cloud.env` (the database URL and the secrets, as on A and B) and
   `/etc/maktaba/cloud.toml`:

   ```toml
   [server]
   env = "production"
   listen_addr = "<NODE_ADDR>:8090"
   admin_listen_addr = "<NODE_ADDR>:8091"
   admin_host = "admin.maktaba.tv"
   user_hosts = ["api.maktaba.tv", "app.maktaba.tv", "maktaba.tv"]
   trusted_proxies = ["<the LB's private address>/32"]
   drain_delay = "12s"

   [database]
   url = "postgres://…"          # PgBouncer once 50.18 lands
   direct_url = "postgres://…"   # the primary itself: the worker's lock and LISTEN
   ```

   then `sudo deploy/cloud-ha/setup.sh --role api` on each: UFW (22 from `SSH_FROM`; 8090, 8091,
   9090 and 9100 from the private network only), the api and worker units (always on: no leader
   to wait for), the worker's control port on the private address for the witness's Prometheus,
   and cloudflared where `TUNNEL=yes`. List the nodes for Prometheus in
   `/etc/prometheus/targets/api-nodes.json` on the witness (`[{"targets": ["<addr>:9090"]}]`).
3. **The Tunnel's ingress** to the balancer's private address (`deploy/cloudflare/tunnel.maktaba.tv.json`).
4. Stop the api and worker on the Phase 1 pair once the pool serves.

**Deploys** go one node at a time with [`bin/api-rollout`](bin/api-rollout) `<tag>`
(`API_NODES="api-1 api-2 api-3"`): each node gets `app-update` over SSH, drains, restarts, and
must stay ready for 15 s before the next. A node that never gets there stops the rollout; the
nodes after it keep their release. **Rollback:** point the Tunnel's ingress back at the Phase 1
leader's loopback and start its api and worker; the code is the same on one node, so it stays.

The rehearsal's `api-pool` (`tools/rehearsal-cloud/compose-pool.yml`, HAProxy standing in for
LB11) proves it: the lock moves within 15 s of its holder's kill, the sign-in limit holds
across the nodes, a plan change reaches both relays within a second, and a rolling deploy fails
no request with each stopping node out of the balancer within 10 s.

## The database on its own nodes (story 50.18, Phase 2)

Phase 2 moves Postgres off the pair onto three machines of its own, so a database node can go
without taking an app node with it. Nothing here is deployed until the owner creates it.

```
app nodes (relay, api, worker) ── db.internal:6432 = the database's Floating IP
                                       │
              ┌────────────────────────┼────────────────────────┐
           db-1 (FSN1)              db-2 (NBG1)              db-3 (HEL1)
      Patroni + PgBouncer      Patroni + PgBouncer      Patroni + PgBouncer
           primary            synchronous replica        async replica
              └──────────── etcd on the three ─────────────────┘
```

- **No committed write lost.** `synchronous_node_count: 1`: a commit returns once one replica
  has it, and Patroni promotes only that replica. With it down for maintenance the other
  replica becomes synchronous, so writes go on.
- **PgBouncer** on every node next to Postgres (`pgbouncer/`, port 6432, TLS required, its
  certificate signed by the owner's CA for `db.internal`, which the apps check with
  `sslmode=verify-full`: the Floating IP is a public address; its own hba rules let the
  superuser in from the node alone and the app role over TLS only): the
  cloud's `database.url` is the transaction pool (`db.internal:6432/maktaba`), and
  `database.direct_url` (the worker's lock, the relay's LISTEN) its session pool
  (`db.internal:6432/cloud_session`). The cloud sends every query with arguments in one round
  trip (`binary_parameters=yes`, which `db.Open` adds): lib/pq's two round trips can land on two
  server connections in a transaction pool. The migrator's lock is a transaction lock, so
  `migrate up` goes through the transaction pool too.
- **Following the leader.** The new primary's callback moves the database's Floating IP
  (`DB_FLOATING_IP_ID`) and starts nothing else (`CLOUD_PROCESSES=off`); a demoted node's
  callback drops its PgBouncer's clients (`KILL` and `RESUME`), so no app stays on a read-only
  server. The apps' pools reconnect through the Floating IP on their own.
- **Backups** run from whichever database node leads (`maktaba-backup.timer` on the three, as on
  the pair: a replica's run does nothing).
- **Alerts:** `pgbouncer_down` (sev1, the primary's PgBouncer, [runbook](../../docs/runbooks/cloud/pgbouncer-down.md))
  and `sync_replica_missing` (sev2, [runbook](../../docs/runbooks/cloud/sync-replica-missing.md)).
- **Planned work:** `sudo switchover --announced --db` picks the synchronous replica and checks
  its PgBouncer, not a Caddy.

**What the owner creates** (none of it is called from the repository):

1. **db-1, db-2 and db-3** (CCX13 or larger, in FSN1, NBG1 and HEL1) in the private network,
   each with `/etc/maktaba/cloud-ha.env` (`NODE_NAME`, `NODE_ADDR`, `SERVER_ID`, `PRIVATE_NET`,
   `SSH_FROM`, the Postgres passwords and backup repositories as on A and B,
   `DB_FLOATING_IP_ID` and `DB_FLOATING_IP`, `DB_CLIENTS`, `JOIN=later` for the move), etcd
   certificates from `bin/make-etcd-certs` for the three, and the Hetzner token in
   `/etc/maktaba/hcloud-token`; then `sudo deploy/cloud-ha/setup.sh --role db` on each.
   **PgBouncer's certificate:** `bin/make-pgbouncer-cert <the etcd CA's dir>` (one certificate
   for `db.internal`, signed by the same CA); `pgbouncer.crt` and `pgbouncer.key` to
   `/etc/pgbouncer/tls.crt` and `tls.key` on A, B and the three database nodes before
   setup.sh (it refuses without them), and `ca.crt` to `/etc/maktaba/db-ca.crt` on every app
   node (A and B included: the move's `repoint` refuses without it).
2. **The database's Floating IP** (in the database nodes' location group), and its id and
   address in A's and B's `cloud-ha.env` too (`DB_FLOATING_IP_ID`, `DB_FLOATING_IP`).
3. **The Cloud Firewall** rule letting the app nodes' addresses reach 6432 on the Floating IP
   (UFW on the nodes allows `DB_CLIENTS` and the private network only).
4. **`db.internal`** in `/etc/hosts` on every app node, pointing at the Floating IP.
5. **Prometheus targets** on the witness: `/etc/prometheus/targets/db-patroni.json` (the three
   on `:8008`) and `db-pgbouncer.json` (the three on `:9127`).
6. **The move:** `deploy/cloud-ha/bin/migrate-db-nodes all --announced` (after the status page
   notice, 50.11), with `RELAY_HOST_A`, `RELAY_HOST_B`, `DB_HOSTS="db-1 db-2 db-3"`,
   `WITNESS_HOST` and the SSH key as for the runbooks. Each step checks first and refuses with
   the reason; `all` can be run again after a refusal. Rollback until `retire`:
   `switchover --db --candidate <A's member>` and repoint back.

The rehearsal proves it (`compose-db.yml`, a second stand-in Hetzner moving the database's
Floating IP). `db-nodes-failover` (three runs): with the synchronous replica stopped the other
took over in 4 to 11 s (writes held at most 2 to 9 s); the primary killed under load, the
synchronous replica led in 29 to 38 s and held the Floating IP 6 to 7 s later, no acknowledged
commit lost (0 of 379 to 482), no relay request failed, the api unready while no primary
existed (35 to 43 s), the workers' lock back in 40 to 47 s, and a backup from the new leader
restored exactly to a point in time from the second repository. `db-nodes-migrate`: the whole
move in 2 min 36 s under 100 homes and a writer, writes held at most 2.5 s, remote play never
paused (0 of 2,490 requests failed), 0 of 1,601 commits lost; db-1 leads with db-2 synchronous
and db-3 streaming, the pair gone from Patroni and etcd.

## Day to day

| What | How |
|---|---|
| Who leads, how far behind the replica is | `patronictl -c /etc/patroni/patroni.yml list` |
| Planned node work | `sudo switchover --announced` (after the status page notice, 50.11): refuses unless the replica streams with zero lag and its Caddy answers |
| Keep a node from leading while it is looked at | `patronictl -c /etc/patroni/patroni.yml edit-config` → its `nofailover` tag; or `patronictl pause` to stop automatic failover altogether |
| The Hetzner API is down and the IP did not move | the DNS-swap runbook (50.12): point the relay names at the new leader's own IP through Cloudflare (TTL 60 s) |

## Known limits

- **A commit cancelled while it waits for the standby is done on the primary only.** Postgres
  reports it committed ("canceling wait for synchronous replication") although the standby
  never got it; if the primary is then lost, so is that commit. The rehearsal's writer found it
  during a partition. Every write path of the cloud with a short deadline (the meter's flush)
  can see it during a partition; the bytes of one flush are the most it costs today.
- The witness runs etcd only; 50.10 adds monitoring and the canary home as lower-priority
  guests (`CPUWeight`), 50.09 the restore drill.

## The relay pool

Phase 2 (story 50.16): several relay machines in one pool, off until `[relay.pool] enabled =
true`. Adding a relay, taking one out and the witness's DNS round robin: [`relay-pool/`](relay-pool/README.md).
