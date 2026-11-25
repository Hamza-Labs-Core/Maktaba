---
schema: 1
kind: pr
title: "The first QA runs from main, and a power cut that leaves Docker a container it cannot remove"
date: 2026-11-25
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
pr: 1708
---

## What happened
- After #1628 merged, QA was deployed twice from main through the detached rollout (v0.3.162, runs 45542944615 and
  45544255325). Each applied, passed TLS, health, firewall, backup and restore, and account, brought the
  released home online through the relay, and failed two checks: `broadcast-off` (v0.3.162 is older than
  #1624 and #1657) and `licence` (decision `cloud-license-key`). Each then rolled back to what QA served. No pass
  was recorded.
- #1708 is the delta review's minors: a rollback that can't reach Docker said "stopped" while the release
  served, and others.
- **Its first version made an older fault likely, and the reboot machine caught it.** Stopped containers are
  now removed; a power cut seconds after a removal can bring the container's folder back without its
  layer. Docker 29 then lists a container that inspect, start and `rm -f` answer "No such container" for, and
  `docker compose ps`, `up` and `up --force-recreate` fail for the whole stack. Every recreate has that window.
  Only removing the folder under `/var/lib/docker/containers` and starting the daemon again ends it.
- The branch's run before the one that caught it had passed: a power cut's moment is chance. #1722 is the
  same leftover, seen on main and on another branch (there, the container of `compose run --rm migrate`).

## What we learned
- Reproduce a Docker state alone before fixing it (a privileged `docker:29-dind` on a runner, two minutes):
  it showed that the obvious fix (`rm -f` and retry) can never work.
- What a power cut can leave is worth making by hand in a case; chance finds it only now and then.
- `docker compose` treats one broken container as a broken project.

## Keep doing
- When a rehearsal fails now and then, find the line that differs before calling it flaky: here it was a
  node nobody could have deployed to again.

## Open
- QA's full pass: a release with #1624 and #1657, and `cloud-license-key`. #1707.
