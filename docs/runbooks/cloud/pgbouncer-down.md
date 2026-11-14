# Runbook: PgBouncer is down on the database primary

> Epic 50 story 50.18.

## When

`pgbouncer_down` (**SEV1**): the PgBouncer on the node Patroni calls the primary has not answered
its exporter for a minute. The database's Floating IP (db.internal) lands on that PgBouncer, so
the api, the relays and the workers can't reach the database: sign-in and new remote sessions
fail; streams already relayed keep going on the relay's cache. Patroni doesn't watch PgBouncer,
so it fails nothing over by itself.

## Check

1. The apps' database errors in the service admin's cloud log view ([api errors, last 30 min](https://admin.maktaba.tv/logs?service=cloud.api&level=error&last=30m)): `connection refused` to db.internal:6432.
2. Which node leads: `patronictl -c /etc/patroni/patroni.yml list` on any database node.
3. On that node: `systemctl status pgbouncer` and the last lines of `/var/log/pgbouncer/pgbouncer.log`
   (a full disk, a bad config after a deploy, `max_client_conn` reached).
4. `pg_isready -h 127.0.0.1 -p 6432` on the node, and `pg_isready -h 127.0.0.1 -p 5432` (Postgres
   itself answers?).

## Do

1. Restart it: `sudo systemctl restart pgbouncer`. The apps reconnect by themselves.
2. It doesn't stay up, or the node is sick: Actions → **Cloud switchover** (the database nodes'
   `switchover --announced --db`, which checks the candidate's PgBouncer first). The database's
   Floating IP follows the new primary within seconds; writes pause up to 15 s.
3. `max_client_conn` reached: an app is leaking connections. Find it with
   `psql -h 127.0.0.1 -p 6432 -U postgres pgbouncer -c 'SHOW CLIENTS'` and restart that app.

## Done when

`pgbouncer_down` is quiet and the api's `/readyz` answers 200 (its database check passes).

## Tell users

Over 5 minutes: an incident on the status page ([templates/](templates/), "identified"), saying
sign-in and new remote sessions are failing and playback in progress is not affected.
