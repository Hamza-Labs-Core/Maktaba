# Postmortem: <title> (<yyyy-mm-dd>)

> Template, Epic 50 story 50.12. Every SEV1 and every automatic failover gets one within **5
> days**, as `docs/postmortems/<incident id>.md` (the incident's file name under
> `status/data/incidents/`, without `.md`). Blameless: what happened and why, never who. A
> summary goes on the incident's status page entry (never a user, a home or an address), and
> the trail gets a line in a trail-only pull request.

## Summary

Two or three sentences: what users saw, for how long, and why.

## Impact

- Minutes of downtime (from the probe), and the month's error budget used (43.8 minutes is 100 %).
- Homes affected (a count, never names), requests failed, data lost (none, or how much).

## Timeline (UTC)

| Time | What happened |
|---|---|
| HH:MM | The first sign (an alert, a report) |
| HH:MM | Acknowledged |
| HH:MM | Status page posted |
| HH:MM | The cause found |
| HH:MM | Fixed; the probe green |

## Cause

The chain of causes, down to the one we can change.

## The evidence

The cloud log searches and error groups (Epic 53.17) that show it: their links, and the lines
that matter (no secrets, no user data).

## What went well

## What didn't

## Actions

| Action | Owner | Issue |
|---|---|---|
| | | #… |
