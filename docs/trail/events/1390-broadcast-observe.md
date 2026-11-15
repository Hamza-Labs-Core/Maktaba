---
schema: 1
kind: pr
pr: 1390
title: "Broadcast rehearsal: alerts, canary probe and status page proven against the real edge"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1390 merged as b6ed73832. Story 50.28's alerts, canary probe and the status page's
  Broadcasting component were in the repo but never ran against the real edge and home. The
  `broadcast-observe` scenario now starts the witness's Prometheus, Alertmanager and
  Pushgateway with the production rules and routes unchanged, folds the edge's analytics events
  into the `maktaba_broadcast_*` series with the cloud's own code (`FoldMinute`, extracted from
  `PollMinute`), plays the canary from both networks every minute and updates the status page.
  Its line left `expected-failures.txt`.
- Each fault fired its alert to the right receiver: cost 80 % (email), glass-to-glass high
  (email), revocation list stale (phone), ingest lag high (phone), canary down (phone). The
  canary page came 21 s after the second failed round; Broadcasting showed outage 39 s later.
  Branch nightly 43679503131 passed all 12 scenarios.
- Also fixed: the monitoring `compose.yml` repeated a service under top-level `volumes:`, so
  `docker compose` refused it and the witness's monitoring could not have started.
- Spec 50.28 now says Broadcasting shows outage (not degraded) when both networks fail, which
  is what the status page does.
- `BROADCAST_CODE` in `tools/release/next_tag.py` gained the alert rules, routes, probes and
  status update, matching the workflow's path filter: a change to them now needs a green
  nightly before a tag.

## What we learned
- Rules and routes that only pass `promtool`/`amtool` can still sit behind an invalid compose
  file; run the real stack once.
- The release gate keeps its own copy of the rehearsal's path filter, and a test compares them:
  widen both together.
- The author child kept working its PR after the review watcher took it; stop the author when a
  watcher takes over, so one agent owns the branch.

## Open
- The nightly on main was dispatched on b6ed73832 after the merge; the next tag waits for it.
- Owner actions: real ntfy and SMTP settings in `cloud-ha.env`; `BROADCAST_PASS_URL` for the
  production canary probe.
