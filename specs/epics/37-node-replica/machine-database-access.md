# A machine's access to its home's database: the home owns it

> **Status:** built with the PR that adds this file, after the outage of 2026-10-24. The Mac
> home went from 0.3.0-rc.5 to 0.3.0-rc.7, and its database moved from an operator Postgres on
> `192.168.50.177:5432` to the app's own on `:54329`. No machine role came along: a dump
> carries no roles, and the dev-to-app move only fenced them and warned. So a GPU node lost
> the home's database: its role failed the password, and a non-TLS try found no `pg_hba` entry.
> The node fell back to offline TV, while the home still showed it as fine because its machine
> agent was connected.

## What happens now, by itself

1. **The machine checks.** Every 30 s, 5 s after it starts, and each time its machine agent
   connects to the home again, a full node logs in to its home's database with `node.json`'s
   access, as its programs do. Its machine agent's `update.state` says how it went:
   `home_db: {ok, code, detail, at, healing, fails, target, home_host}`.
   - `code` is one of `auth_failed` (password, role missing, `NOLOGIN`), `no_hba`, `refused`,
     `unreachable`, `no_database`, `tls` or `other`. A refused password on the TLS try stays
     the reason over a plain try's missing `pg_hba` line.
   - `target` is where it logs in: `host:port/database`, never the password; `home_host` the
     host it reaches the home's API at. `detail` never carries a login's secrets either (a url's
     user and password, a `password=`).
   - `fails` counts the failed logins in a row. After a failure the machine tries again in
     10 s, and a failure counts once it is seen twice: one try can meet a home in the middle of
     a switch (the machine still on the old home's database, about to follow the new home by
     itself), and the new home must not re-make its access then.
   - On a new connection to the home the last check is forgotten until a new one is made (a
     failure while the home was away is not the home's to act on).
   - A change is sent at once.
2. **The home re-makes the access, where it mends the login.** Every 10 s the home
   (`agent.Hub.EnsureAccess`) looks at its connected machines. A machine gets its access re-made
   when, twice in a row:
   - its password is refused or its role is unknown (`auth_failed`);
   - or its `target` is not the access the home hands out, whatever the old address does now
     (refuses, closes the connection, fails TLS): the database moved. Another port, another
     database, or another host: the one the home names, else the one the machine reaches the
     home's API at (`home_host`), so a move to another machine on the same port and database
     name is seen too (a machine that reaches the home by another name than its database's gets
     one re-made access, then points at the home's name);
   - or its role does not log in on the home's current database (checked once per agent
     session: a move that carried no roles).

   At the right address, TLS failures and anything else are not the access's to mend, and a
   database that refuses or cannot take a login there (too many clients, starting up, a
   `pg_hba` of the operator's) is not rotated. A home whose database cannot say the cluster's
   term re-makes nothing (an access with no term would be refused, its password changed). A re-made access that did not help is tried again after a
   minute, then two, four, … up to an hour, until the machine logs in.
   - The machine's role is made again with a new password (`PGRoles.Create`: made, or
     re-enabled with `LOGIN`, in the worker group). The statement carries the password's SCRAM
     verifier, never the password: no DDL log or `pg_stat_activity` shows it (nor when a role
     or a copy's replication role is turned off). No password is kept anywhere to recover.
   - The access is pushed over the machine channel, which the cluster key authenticates:
     `home.access {role, password, db_host?, db_port, db_name, database_ca?, database_tls?,
     term}`. The command goes to the machine's own agent only (the processes page's command
     endpoint takes the processes' own commands only: drain, undrain, reload_stages, log_level,
     disconnect), and the hub's event log never shows the password.
   - The port is the join's and the re-made access's alike: the bundled Postgres's
     (`MAKTABA_CLUSTER_PG_PORT`); else `MAKTABA_CLUSTER_DB_PORT`, the port the machines reach
     when it differs from the database's own (the compose files pass `MAKTABA_PG_PORT`, the
     published port, through it); else the one the database says it listens on
     (`inet_server_port()`: an operator's Postgres on another port than 5432); else 5432.
   - `db_host` is set only when `MAKTABA_CLUSTER_DB_HOST` names the database's host (one on
     another machine than the home); otherwise the machine uses the host it reaches the home's
     API at, as at its join. The home's own address for its database (compose's `postgres`, a
     loopback) is never handed out.
   - The CA is the home's, as a join's credentials carry it; a bundled home whose CA cannot be
     read re-makes nothing (an access without it could not be verified). The CA push
     (`home.database`) keeps it current as before.
3. **The machine follows it.** `node.json` is changed under its lock (`config.UpdateNodeConfig`,
   the user-only file and ACL as always). The log says "the home's database moved, or this
   machine's access to it was re-made: following it at <host:port> as <role>". The machine
   marks itself `healing` and starts again with the new access.
   - An access without a CA and without `database_tls: off` keeps how the machine verifies the
     database now: never a downgrade to an unverified connection.
   - A host or database name that is not one is refused.
   - An access from a home of an earlier term than the one `node.json` follows (a home that has
     not learned of a switch yet), or one that arrives while a switch of the home runs on the
     machine or after it follows a new home (`home.moved`), is refused.
4. **The pages say why.** A node that cannot log in (twice in a row) shows on Admin → Updates
   and on the Machines page as "This machine can't reach the home's database: <reason>". The
   detail follows, with "the home gave it new access; it is following it" while it heals, and
   a link to its logs. It is not shown as connected and healthy meanwhile.
5. **An update carries its machines along.** A home's update that moves its database waits in
   its watch for its machines to follow it (the home's `/api/cluster/self` lists them,
   `machines_db`, to the home's own supervisor only: its local token). Each machine
   holds the home's update for 10 minutes at most from the first time it said so in that watch,
   however often it starts again meanwhile; a machine still unable after that no longer holds
   it, and the pages show it as needing a person.

## TV

A node's channel engine heartbeats into the home's database (`tv_host_beat`) through the same
access. A node that cannot log in stops beating, and the balancer counts it down after
`dead_after` (`missed_beats`), then re-places its channels on another host or on the home at
their next request (`ReasonFailover`, `streaming/internal/placement`). Once the node follows its
new access, it beats again and takes channels again. The pages no longer show such a node as
fine just because its machine agent is connected.

## Tested

- **End to end, on real Postgres** (`api/dbmove_e2e_test.go`), with SCRAM for the machines'
  roles (the replica job's `pg_hba`), both halves of the outage in turn: the node's address
  refuses (nothing listens there): the home re-makes its access because it points elsewhere
  (`refused_moved`) and the node follows the real address (51 s on Windows); then its role is
  dropped: its password refused (`auth_failed`), its role made again, and it logs in with the
  pushed password (70 s), all by itself.
- **The node:** the access taken (the host the home is reached at, the port, the role, the CA;
  the other fields and the url's settings kept), refused when bad, and the same access
  changing nothing; the classification of every login failure; the watch.
- **The home:** a missing role re-made and pushed; a healing machine left alone; the password
  out of the event log; `GrantAccess` and `AccessUsable` on approved, bundled and revoked
  machines; a node's view carrying its live state.
- **The home's watch** waits for its machines, and stops waiting after 10 minutes.
