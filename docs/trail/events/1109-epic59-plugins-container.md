---
schema: 1
kind: pr
pr: 1109
title: "59.07: plugin isolation levels, the container-only hold, and plugind's --sandbox-check"
date: 2026-11-10
session: eb60fee0-892c-48d2-9d5e-340bf67c6fec
agent: claude-code
---

## What happened
- #1109 (merged at e5f5013d7) shipped story 59.07. Each machine computes a plugin isolation level, Full or Container, from 59.04's Landlock and user-namespace facts and from plugind's report. It writes the level to `app_settings` `plugins.isolation` (machine → level) and shows it on Settings, This machine and on the Plugins page.
- On Container, third-party plugins wait with `isolation_held` (the Go and Python gate twins, the shared table and the catalog reasons). They run once an admin turns on the per-home switch `allow_container_isolation` (`GET`/`PUT /api/plugins/isolation`, admin only, password typed again, audited, with role tests). The None level is still 59.01's preflight refusal. First-party runners keep running, reported `sandbox_partial`.
- The trash follow-up: a runner grants the nearest existing folder at or under the library root (`trash_grant`) and creates nothing in a library, so a trash made after the runner started is still covered.
- The NAS no-Landlock cells assert the level, the hold, the 401 without the password, the 200 with it, then a running plugin and `plugins.host` Degraded `sandbox_partial`. The amd64 image job packs plugind's hello guest as the `nas-plugin` artifact. `landed` gained 59.07.
- The update rehearsal (scenario 15) found a real gap. On GitHub's Ubuntu runner the server's probe (`clone(CLONE_NEWUSER)`) said Full, but plugind reported `namespaces` missing: AppArmor lets the namespace be made and refuses its uid map. So each start began at Full, a third-party plugin ran, and only then did plugind's report drop the level to Container and hold the plugin. Fix: `maktaba-plugind --sandbox-check` applies a start's layers, prints `{sandbox, missing}` and exits, with no socket and no step file. The plugins runner asks it once on Linux, before its first level. The rehearsal's admin now turns the switch on, as any Container-level home's admin would.

## What we learned
- A probe that only resembles a sandbox's steps can disagree with it. Ask the component that applies the sandbox (here, plugind itself) rather than re-deriving its answer elsewhere. A shell child cannot replay plugind's in-process unshare and map writes either: a process exec'd unmapped into a new user namespace cannot write its own `/proc/self` maps.
- GitHub's ubuntu-24.04 runners are a Container-level machine for plugind (no usable unprivileged user namespaces). Any CI that runs third-party plugins through the full server needs the switch on.
- `/api/components`' list `reason` is a false condition's reason only. A Degraded row keeps Ready true, so `sandbox_partial` shows only in `GET /api/components/{id}`'s part conditions.
- Every `download-artifact` step needs the 3-attempt chain and its check step (`tools/ci/tests/test_artifact_download.py`). A `::warning::` check marks the download optional.
- A conflicted pull request runs only part of CI: the NAS and rehearsal workflows did not start until main was merged in.

## Keep doing
- Read the cell's and the rehearsal's log artifacts before deciding a failure is a flake. Both failures here were real, one in the test and one in the product.

## Open
- The Components list shows Degraded without a reason (no `sandbox_partial` beside it), because the list's reason comes only from false conditions. This predates 59.07.
- plugin routes are still absent from `openapi.json` (59.16).
