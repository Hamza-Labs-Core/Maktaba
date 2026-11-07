---
schema: 1
kind: pr
pr: 882
title: The cloud worker exited at start since June (two listeners on its control port)
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [877]
---

## What happened
- `runWorker` started its control server with `go srv.ListenAndServe()` and then gave the same server to `server.Run`, which listens again. The second bind failed (`address already in use`), and the worker exited 1 at start.
- It had been so since c15649f76 (June, Epic 25). No worker job ever ran: expired-token cleanup, deletion purges, the relayed-stream sweep, pay-as-you-go usage reports and the direct-access certificates. The cloud is not live, so no account was affected.
- The Epic 50 session found it while building the `direct-remote` rehearsal, where the worker has to issue certificates. It was recorded in #877's event and handed over unfixed at the usage limit; this PR fixes it.
- `server.Run` is now the only listener. `TestWorkerRoleStaysUp` re-runs the test binary as `serve --role worker` on a scratch database, and checks it answers, stays up 3 s and exits 0 on SIGTERM. On the old code it fails with the bind error.

## What we learned
- Each job had a unit test and the role's start had none. Every test called the jobs directly, so a role that could not start passed every test for five months.
- The same gap hid a second bug on the same day: `config.Validate` refused `--role dns`, so the dns role could not start either (fixed with the `direct-remote` rehearsal).
- `server.Run` owns the listener. A role that also starts its own server double-binds.

## Keep doing
- Give every role and binary a test that starts its real process once, and checks it stays up and stops cleanly, before its deploy ships. The child-process pattern here (re-run the test binary with one test selected and an env var) is cheap: 5 s.

## Open
- The dns role gets the same child-process check in the `direct-remote` rehearsal's PR.
- Nothing installs or updates `/usr/local/bin/maktaba-cloud` on the nodes, which the api, worker and dns units run; that is being fixed next.
