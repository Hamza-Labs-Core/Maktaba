# Runbook: the cloud's errors spike, or a node's logs stop reaching the witness

> Epic 50 story 50.10, Epic 53.17.

## When

- `cloud_error_spike` (**SEV2**).
- `log_forward_lagging` (**SEV3**).

## Check

The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the error groups say which code path; the saved search `errors by route, last hour`.

## Do

1. `cloud_error_spike` within an hour of a deploy: [deploy-rollback.md](deploy-rollback.md).
2. Otherwise: open an issue with the error group's link, and fix forward.
3. `log_forward_lagging`: the node's forwarder can't reach the witness (the private network, the witness's disk: [disk.md](disk.md)). Its logs stay on the node (14 days) and are sent when it can again.

## Done when

The error rate is back to its usual level, or the forwarder's lag is under a minute.

## Tell users

Only if users saw it: [canary-down.md](canary-down.md)'s.
