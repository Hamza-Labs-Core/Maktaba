# Runbook: a deploy is bad after it passed its checks

> Epic 50 stories 50.08, 50.12.

## When

Errors or complaints after a release that passed its health checks and the blue/green watch: `cloud_error_spike` (**SEV2**) within an hour of a deploy, or the canary failing after one.

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the error groups new since the deploy.
2. The tag running: `cat /opt/maktaba-cloud/deployed` on the leader; the previous one is the last good release on the Releases page.

## Do

1. Actions → **Relay CI/CD** → Run workflow with the previous tag. It runs `deploy/cloud-ha/deploy.sh` (blue/green, 50.08): homes move to the old release without a gap.
2. **Never roll the schema back**: migrations are expand-only (50.08), so the old release works on the new schema.
3. Open an issue for the bad release and keep it from being deployed again (the next tag fixes it).

## Done when

The previous tag runs (`/opt/maktaba-cloud/deployed`), the error spike is gone and the canary is green.

## Tell users

Only if users saw it: [canary-down.md](canary-down.md)'s.
