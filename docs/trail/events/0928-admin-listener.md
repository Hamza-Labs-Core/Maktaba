---
schema: 1
kind: pr
pr: 928
title: The operator console on its own listener and name
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [883]
---

## What happened
- Story 50.32 part 1. The api role runs two listeners:
  - the user listener answers only `server.user_hosts` and has no admin route;
  - the admin listener answers only `server.admin_host` and serves `/v1/admin/*`.
- Either listener answers a wrong Host with chi's unknown-route 404. The probes answer on any Host.
- The admin listener refuses preflights. A change on it needs the console's Origin and `X-Maktaba-Staff: 1`, else `403 bad_origin`.
- The refresh cookie is `__Host-maktaba_refresh`.
- The relay's access log prints its own resolved client IP, as promised on #883.
- The web console sends the staff header on every change, the refresh included.

## What we learned
- Moving the admin routes off the user router also took away the console's sign-in: `/v1/auth/*` and `/v1/account/me` lived only on the user listener. Until 50.33's `/v1/staff/*` exists, the admin listener serves exactly those four routes (`ConsoleSignIn`). The routers test (T03) allows that list and nothing more.
- `validateAdminListener` must let two `:0` addresses coexist, or the test harness can't start both listeners.
- The integration harness keeps the old one-handler `e.h` working with a dispatcher. It sends admin paths to the admin router with the console's Host, Origin and header set, so the older role tests didn't need rewriting.

## Keep doing
- Prove a split by walking both routers (`chi.Walk`), not with a hand-written list.

## Open
- 50.32 §3: the `cloud-web` and `admin-web` Workers, attached as Workers Routes (never Custom Domains), with the per-host CSP and their Miniflare tests (T08).
- 50.33 removes `ConsoleSignIn`.
