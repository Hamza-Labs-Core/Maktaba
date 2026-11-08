---
schema: 1
kind: incident
title: The Mac update to 0.3.96 left the server stopped behind a login item macOS would not re-register
date: 2026-11-08
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- The live self-update 0.3.90 → 0.3.96 (Developer ID, team G58UVTKQPB, macOS 27) stopped the server cleanly for the install, then failed at relaunch (`relaunch_failed`). app.log: the old app's restart after the install hit `SMAppServiceErrorDomain 22` (Invalid argument), the new app's settle hit `SMAppServiceErrorDomain 1` (Operation not permitted), and every later launch showed "The Maktaba server could not start." with error 1, even with Maktaba on under Login Items.
- launchd still had the job the whole time (`launchctl print gui/$UID/com.hamzalabs.maktaba.server`: loaded, submitted by smd, not running, last exit 0). A manual `launchctl kickstart` started 0.3.96 and the home came up. The registration was valid; only the app's unregister + register dance failed.
- Root cause in `apps/desktop/src-tauri/src/agent.rs`: `register_login_item` unregistered and registered again whenever the executable's stamp changed (added for ad-hoc builds, whose code hash changes per build), and `control()` returned on that error before it ever ran `launchctl kickstart`. So the restart after the install, the relaunch and every app start all stopped at the refused registration and never started the job. Error 22 came from the old process: after the bundle swap, `current_exe` pointed at the new build's file, so the old app tried to re-register on behalf of a bundle it was no longer running.
- Fix: a pure `login_item_step` decides what the login item needs. A Developer ID build (Team ID read from `codesign -dv`) keeps its registration across updates and re-registers only when the bundled plist changes; a silent server after an update gets a kickstart, not a re-register. A process whose executable changed on disk since it started (`remember_this_build`) leaves registration to the new build. Any refused registration goes through `on_refusal`: Enabled or job loaded → kickstart and wait for launchd to run it; RequiresApproval → the settings prompt; otherwise the error stands.

## What we learned
- SMAppService errors on register/unregister do not mean the login item is gone. Check `status` and `launchctl print` before treating a refusal as fatal; launchd may still hold a perfectly good job.
- A Developer ID app's designated requirement (anchor, team, identifier) survives updates, so its SMAppService registration does too. Re-registering on every update is only needed for ad-hoc signed builds.
- After the updater swaps the bundle, the still-running old process reads the new build's executable through `current_exe`. Anything keyed on "this build" must be captured at process start.

## Stop doing
- Do not put a registration step in front of `launchctl kickstart` with `?`: a refused registration then blocks starting a job that would have run.
