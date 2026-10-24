# A machine's access to its home's database: the home owns it

> **Status:** built with the PR that adds this file, after the outage of 2026-10-24. The Mac
> home went from 0.3.0-rc.5 to 0.3.0-rc.7, and its database moved from an operator Postgres on
> `192.168.50.177:5432` to the app's own on `:54329`. No machine role came along: a dump
> carries no roles, and the dev-to-app move only fenced them and warned. So a GPU node lost
> the home's database: its role failed the password, and a non-TLS try found no `pg_hba` entry.
> The node fell back to offline TV, while the home still showed it as fine because its machine
> agent was connected.

## What happens now, by itself

1. **The machine checks.** Every 30 s, and 5 s after it starts, a full node logs in to its
   home's database with `node.json`'s access, as its programs do. Its machine agent's
   `update.state` says how it went: `home_db: {ok, code, detail, at, healing, fails}`.
   - `code` is one of `auth_failed` (password, role missing, `NOLOGIN`), `no_hba`, `refused`,
     `unreachable`, `no_database`, `tls` or `other`.
   - `fails` counts the failed logins in a row. After a failure the machine tries again in
     10 s, and a failure counts once it is seen twice: one try can meet a home in the middle of
     a switch (the machine still on the old home's database, about to follow the new home by
     itself), and the new home must not re-make its access then.
   - A change is sent at once.
2. **The home re-makes the access.** Every 10 s the home (`agent.Hub.EnsureAccess`) looks at its
   connected machines. A machine gets its access re-made when it says it cannot log in (twice in
   a row), or when
   its role does not log in on the home's current database (checked once per agent session: a
   move that carried no roles).
   - The machine's role is made again with a new password (`PGRoles.Create`: made, or
     re-enabled with `LOGIN`, in the worker group). No password is kept anywhere to recover.
   - The access is pushed over the machine channel, which the cluster key authenticates:
     `home.access {role, password, db_host?, db_port, db_name, database_ca?, database_tls?}`.
   - It happens at most once a minute per machine. The command goes to the machine's own
     agent only, and the hub's event log never shows the password.
   - The port is the bundled Postgres's (`MAKTABA_CLUSTER_PG_PORT`), or else the one the
     database says it listens on (`inet_server_port()`), so an operator's Postgres on another
     port is handed right. `db_host` is set only for a database on another machine than the
     home; otherwise the machine uses the host it reaches the home's API at.
   - The CA and TLS are the home's, as a join's credentials carry them. The CA push
     (`home.database`) keeps them current as before.
3. **The machine follows it.** `node.json` is changed under its lock (`config.UpdateNodeConfig`,
   the user-only file and ACL as always). The log says "the home's database moved, or this
   machine's access to it was re-made: following it at <host:port> as <role>". The machine
   marks itself `healing` and starts again with the new access.
4. **The pages say why.** A node that cannot log in shows on Admin → Updates and on the
   Machines page as "This machine can't reach the home's database: <reason>". The detail
   follows, with "the home gave it new access; it is following it" while it heals, and a
   link to its logs. It is not shown as connected and healthy meanwhile.
5. **An update carries its machines along.** A home's update that moves its database waits in
   its watch, for up to 10 minutes, for its machines to follow it (the home's
   `/api/cluster/self` lists them: `machines_db`). A machine still unable after that no longer
   holds the home's update; the pages show it as needing a person.

## TV

A node's channel engine heartbeats into the home's database (`tv_host_beat`) through the same
access. A node that cannot log in stops beating, and the balancer counts it down after
`dead_after` (`missed_beats`), then re-places its channels on another host or on the home at
their next request (`ReasonFailover`, `streaming/internal/placement`). Once the node follows its
new access, it beats again and takes channels again. The pages no longer show such a node as
fine just because its machine agent is connected.

## Tested

- **End to end, on real Postgres** (`api/dbmove_e2e_test.go`): the node's address refuses and
  its role is dropped. The node says so, the home makes its role again and pushes the real
  address, and the node logs in again, all by itself (11 s on Windows).
- **The node:** the access taken (the host the home is reached at, the port, the role, the CA;
  the other fields and the url's settings kept), refused when bad, and the same access
  changing nothing; the classification of every login failure; the watch.
- **The home:** a missing role re-made and pushed; a healing machine left alone; the password
  out of the event log; `GrantAccess` and `AccessUsable` on approved, bundled and revoked
  machines; a node's view carrying its live state.
- **The home's watch** waits for its machines, and stops waiting after 10 minutes.
