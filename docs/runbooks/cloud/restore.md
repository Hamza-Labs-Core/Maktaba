# Runbook: the cloud's whole zone or Hetzner is gone (restore)

> Epic 50 story 50.09 §5. Target: **RTO 4 h**, **RPO ≤ 60 s** (the WAL is archived at least
> every minute). Proven in CI by the cloud rehearsal's `restore-pitr` scenario.

## When

Neither node A nor B can be brought back: the data centre zone is down for hours, Hetzner is
down, or both disks are lost. `restore_drill_failed` (**SEV2**) and `restore_drill_missing`
(**SEV3**) also link here: the weekly drill on W uses the same steps. For one node down, nothing
is needed: the other leads by itself ([failover-check.md](failover-check.md)). **SEV1.**

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17): the
   witness keeps both nodes' last lines.
2. Hetzner's status page and the Hetzner console: are the machines gone, or only unreachable?
3. Both backup repositories answer (`pgbackrest --stanza=cloud info` from W, which has the
   configuration for the drill).

## Do

### 1. Decide where (15 min)

- Another Hetzner zone if Hetzner itself works (Object Storage in HEL1 still readable): restore
  from **repo1**.
- Another provider if Hetzner is down: restore from **repo2** (Cloudflare R2; no egress cost).
- Order two machines (Ubuntu 24.04, at least 2 vCPU and 4 GB each) and a witness, on one private
  network. A Floating IP if the provider has one; otherwise the relay names point at the new
  leader's own address (step 5).

### 2. Put the secrets back (15 min)

From the password manager (or the encrypted copy under `secrets/` in either repository:
`age -d -i <owner key> cloud-secrets-*.tar.age | tar -x`): the token secret, the entitlement key
(the worst to lose: homes must trust a new one after 7 days), the payment provider's and OAuth
secrets, the APNs and FCM keys, both pgBackRest passphrases, the etcd CA key, new Hetzner and
Cloudflare tokens.

### 3. Rebuild the machines (45 min)

`deploy/cloud-ha/README.md`, "What the owner creates", steps 6–9, then `setup.sh --role witness`
on W. **Do not run `setup.sh --role a` yet**: it would bootstrap an empty cluster.

### 4. Restore the database (30 min)

On the new node A, with `/etc/pgbackrest/pgbackrest.conf` rendered by setup.sh (it renders it
before Patroni starts):

```bash
sudo -u postgres maktaba-restore --pgdata /var/lib/postgresql/16/maktaba --repo 1 --target latest --no-start
# or --repo 2 when Hetzner is gone; --target '2026-11-07 01:15:43+00' to stop before a bad write
```

Then `setup.sh --role a`: Patroni finds the data directory and starts it as the leader (it does
not bootstrap over existing data), the callback takes the IP and starts the cloud. Then
`setup.sh --role b` on B: it joins as a replica by a base backup.

### 5. Point the names at it (15 min, plus DNS)

If the new pair has a Floating IP, point the records of `docs/cloud-dns.md` at it in Cloudflare;
otherwise point `relay` and `*.relay` at the leader's own address (TTL 60 s while it lasts). The
API's Tunnel (50.31) follows the leader by itself.

## Done when


- `patronictl list`: a leader and a streaming replica.
- `https://canary.relay.maktaba.tv/api/health` answers; the status page goes green (50.11).
- `maktaba-cloud migrate --config /etc/maktaba/cloud.toml status`: nothing missing.
- Run a backup at once (`sudo -u postgres maktaba-backup full`): the old repository's timeline
  ends here.
- Write the postmortem (50.12) with the times of each step, against the 4 h target.

## Tell users

At once: [templates/investigating.md](templates/investigating.md) on the status page (it is off
Hetzner, so it still works), then an update every 30 minutes with the step reached; email
paying users after an hour. [templates/resolved.md](templates/resolved.md) when the canary is
green. A postmortem within 5 days ([postmortem-template.md](postmortem-template.md)).
