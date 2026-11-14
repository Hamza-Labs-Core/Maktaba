# Runbook: the store reviewers' home fails its check

> Epic 58 story 58.04. review.maktaba.tv is the home every TV store's reviewers sign in to
> ([docs/stores/review-home.md](../../stores/review-home.md)). While it fails, a submission
> under review can be rejected.

## When

- `review_home_down` (**SEV2**): a check of `tools/review_home/probe.py` failed on its last run
  (`check` names it: `discovery`, `signin`, `content`, `streams` or `cloud_code`).
- `review_home_probe_stale` (**SEV3**): no run has reported for an hour (the probe is off, or its
  workflow fails before it pushes).
- A failed run of the **Review home probe** workflow (GitHub mails it).

## Check

1. The cloud's side of the cloud code ([tv codes, last 30 min](https://admin.maktaba.tv/logs?service=cloud.api&q=tv_code&last=30m)): a `cloud_code` failure with errors here is the cloud's, not the home's.
2. The run's log (Actions → **Review home probe**): each check prints its reason.
3. `discovery` fails: is the machine up, and does `https://review.maktaba.tv/api/discovery/home`
   answer? On the machine: `docker compose -f /opt/maktaba-review/docker-compose.yml ps`.
4. `signin` fails right after a submission round: the password was rotated on the home but not
   in the repository secret `REVIEW_HOME_VIEWER_PASSWORD` the probe reads (or the reverse).
5. `content` fails: a new item not titled yet (run `seed.sh seed` again after the scan), or the
   content set changed without a re-run of `setup.sh`.
6. `streams` fails: the home's Admin → Logs for the stream session's error.
7. `cloud_code` fails with the home fine: is the home still registered with the cloud (Admin →
   Remote access), and is its account's plan the one with the relay?

## Do

1. The home down: `cd /opt/maktaba-review && docker compose --env-file .env up -d`.
2. A failed update: Admin → Updates shows the reason; the launcher already went back. Hold
   updates (Defer) while a submission is open.
3. The password out of step: run the rotation again (`seed.sh rotate`) with the values in the
   release secrets, and set the repository secret to the viewer's.
4. Missing titles: `seed.sh seed`.

## Done when

A run of the **Review home probe** workflow passes every check, and `review_home_down` is quiet.

## Tell users

Nobody: no household uses the review home. If a store's review was under way, note the outage
in that submission's reply to the store.
