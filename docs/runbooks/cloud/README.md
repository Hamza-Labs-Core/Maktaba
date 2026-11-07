# Maktaba Cloud runbooks

> Epic 50 story 50.12. Recovery is automatic or one click, never a terminal session at 3 a.m.
> Where a person must act, the page for it is here: short, tested, the same layout every time
> (**When**, **Check**, **Do**, **Done when**, **Tell users**). Every alert (50.10) carries a
> severity and links to one (`tools/runbooks/lint.py` checks both in CI).

## Severities

| Level | Means | Examples | Response | Update users |
|---|---|---|---|---|
| **SEV1** | Paying users can't play remotely, or data is at risk | both nodes down; relay down > 3 min; database lost; secrets leaked | acknowledge in **15 min**, any hour | status page within **15 min**, then every 30 min |
| **SEV2** | Degraded, or one safety net gone | standby down (no failover possible); backups failing > 24 h; sign-in down but relay up; failover happened | acknowledge in **1 h** (07:00–24:00 local), else next morning | status page if users notice |
| **SEV3** | No user impact yet | certificate expires in < 14 days; disk 80 %; cost past 80 % of the ceiling | next working day | no |

**On call** is the owner, alone. SEV1 pages the phone and repeats every 5 minutes until
acknowledged (silenced in Alertmanager); SEV2 pages once; SEV3 is an email digest. The outside
probe (GitHub Actions) mails on its own, so a broken witness doesn't silence a SEV1.

## The runbooks

| Runbook | When |
|---|---|
| [canary-down.md](canary-down.md) | remote play down: the canary, the API, the error budget |
| [failover-check.md](failover-check.md) | an automatic failover happened; the replica is missing or lagging |
| [switchover.md](switchover.md) | planned node work: Actions → **Cloud switchover** |
| [forced-failover.md](forced-failover.md) | the leader is sick but alive: Actions → **Cloud failover** |
| [hetzner-api-down.md](hetzner-api-down.md) | the Floating IP can't move: Actions → **Cloud DNS swap** |
| [restore.md](restore.md) | both nodes lost, or the zone: restore from the backups (4 h) |
| [deploy-rollback.md](deploy-rollback.md) | a deploy is bad after it passed its checks |
| [cert-expiry.md](cert-expiry.md) | a certificate expires in < 14 days |
| [disk.md](disk.md) | a disk is filling |
| [errors.md](errors.md) | the cloud's errors spike; logs stop reaching the witness |
| [backup-stale.md](backup-stale.md) | a backup is late; the WAL archive fails |
| [payments-backlog.md](payments-backlog.md) | payment webhooks fail or lag |
| [abuse-ddos.md](abuse-ddos.md) | a traffic spike, abuse |
| [secrets-rotation.md](secrets-rotation.md) | a secret leaked, or might have |
| [tunnel-down.md](tunnel-down.md) | the API's Tunnel is down |
| [staff-security.md](staff-security.md) | a staff account's security alert |
| [status-page.md](status-page.md) | posting an incident or a maintenance notice |
| [capacity.md](capacity.md) | the relay is filling its node: resize, or start Phase 2 |

The one-click actions (**Cloud switchover**, **Cloud failover**, **Cloud DNS swap**) run
`deploy/cloud-ha/runbook/`, which the cloud rehearsal runs first against its own pair
(`runbook-switchover`, `runbook-forced-failover`, `runbook-dns-swap`). They run in the
`production` environment: its required reviewer approves each run.

The plugin store (Epic 54) has its own alerts and runbooks; a store incident uses these
severities and templates.

## Telling users

[templates/](templates/) has one file per stage (investigating, identified, monitoring,
resolved) and one for maintenance, each in English and Arabic: copy one to
`status/data/incidents/` ([status-page.md](status-page.md)). For a SEV1 longer than an hour,
email paying users the same text from the cloud's mailer.

## Postmortems

Every SEV1 and every automatic failover: [postmortem-template.md](postmortem-template.md),
within 5 days, as `docs/postmortems/<incident id>.md`. An incident marked `severity: sev1` and
resolved over 5 days ago without one opens a SEV3 reminder issue (the **Postmortem reminder**
workflow, daily).

## No downtime credits

The 99.9 % monthly target for remote access drives the alerts, the error budget and the
postmortems, and the status page shows the measured figure. It is a target, not a guarantee:
Maktaba Cloud gives **no credit, refund or discount** for downtime (`uptime.policy`). No page,
email or template offers one; `tools/runbooks/lint.py` checks the templates and the strings.
