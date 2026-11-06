---
schema: 1
kind: pr
pr: 663
title: A restart for components whose loops run in another process
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 636
---

## What happened
- `POST /api/components/{id}/restart` refused (409 not_restartable) every component whose loops run outside the API: livetv.inprocess, livetv.hdhomerun (streaming), queue_health (pipeline), discovery.mdns and backups (supervisor).
- The supervisor's restart route now also restarts its own loops (`Reconciler.RestartLoops`: stopped as a switch-off stops them, started by the next 1 s pass).
- For the hosts the API cannot reach, slot 0185 `component_restarts (machine, id, host, seq)`: the route moves the seq; the write moves `component_meta.revision` with the same `components_changed()` trigger, so the Go runner (API, streaming) and the pipeline's runner hear it on their LISTEN and restart their loops of that component when its seq moved. The first read is only remembered, so a starting process restarts nothing.

## What we learned
- A new table that revokes the worker group's default grants fails compat-prev: the previous release re-makes `maktaba_worker` without knowing the table, and `TestWorkerGroupAsTheMigrationsMadeIt` reads the revoke as a change of the group. No migration has ever been marked "Breaking for the previous release"; keeping the default grants was the way through.
- A new table must also be listed in `copyReviewed` (`api/internal/cluster/copy_tables_pg_test.go`) or excluded from the cluster copy.
- Reusing `components_changed()` as the trigger of another table works because it only reads `NEW.id` (and `node_id` for overrides): the runners' existing wake-up path carried the request for free.

## Keep doing
- Signal another process through the desired-state revision it already watches, instead of a new channel.
