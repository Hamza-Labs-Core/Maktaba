---
schema: 1
kind: pr
pr: 1224
title: "Each port chosen in the container, published ports on a bridge (story 59.08)"
date: 2026-11-12
session: d16acbf7-0958-45de-8c7d-166da1d433f2
agent: claude-code
---

## What happened
- #1224 adds one environment variable per port, applied over `server.toml` on every config load (`cmd/maktaba-server/internal/config/portenv.go`). The variables are `MAKTABA_WEB_PORT`, `MAKTABA_API_PORT`, `MAKTABA_STREAM_PORT` and `MAKTABA_POSTGRES_PORT`. On a bridge, `MAKTABA_PUBLIC_WEB_PORT`, `MAKTABA_PUBLIC_API_PORT` and `MAKTABA_PUBLIC_STREAM_PORT` (`[server] public_*_port`) name the published ports.
- A port a variable sets is never moved. Setup's check and serve stop on it with `setup.check.note.port_setting_taken`, in English and Arabic, naming the port, the holder and the variable. A default port another program holds still moves into 8780–8790, as before.
- The bundled Postgres's port is now checked before Postgres starts. In a container, or when the variable set the port, the start stops with a bilingual message.
- `/api/discovery/home` answers `stream_port`, and a node learns its home's live TV port from it at start.
- Settings → Storage & discovery locks an address whose port is set in the environment.
- Every NAS file shows the variables commented:
  - the published compose's `[ports]` block;
  - the Synology files, regenerated;
  - an advanced field per port in the Unraid template;
  - the TrueNAS custom app.
  
  The TrueNAS catalog app passes its published port numbers as `MAKTABA_PUBLIC_*_PORT`.
- New nas-shapes cells: `custom-ports`, `bridge-published-ports` and `port-taken`.

## What we learned
- The task's premise was that nothing moves a taken port and the compose comment was wrong. That was not true. `ReconcilePorts` (`config/ports.go`) has moved a taken web, API or live TV port since before Epic 59, and story 59.08 made the move visible (`port_moved`). The comment was right; it now also points to `[ports]`. Grep for the behaviour (`ReconcilePorts`, `port_moved`) before accepting "not found".
- Two places assumed default ports regardless of the config:
  - `supervisor.HomeStreamURL` hard-coded 8081 for a node's home. A home whose live TV port had moved off a taken 8081 already broke its nodes' TV front door before this change.
  - The launcher's can't-start page hard-coded 127.0.0.1:8088.
- An env override must not leak into `server.toml`. Setup and Settings save the whole config, so `fileTOML` restores the file's own values for every pinned key. Otherwise removing the variable would not go back.
- Unraid passes a template's empty variable as `""`, so an empty port variable counts as unset.
- A new home has the TV tuner (`livetv.hdhomerun`) off, so `/discover.json` answers 409 `component_off` on the streaming port. The `custom-ports` cell's first run failed only on that; a live TV check turns it on first (`POST /api/components/apply`).
- Postgres is not the supervisor's `ReconcilePorts` business. A held 54329 only showed as a `port_taken` retry loop, in English only.

## Keep doing
- Read every port from the config (`cfg.Server.Listen`, `cfg.PublicWebPort()`), never a literal.

## Open
- The browser HTTPS port (8443), direct access (8444) and the TV slot ports (8791–8793) are still fixed.
- The TrueNAS catalog app has no host-network port questions: Maktaba's own ports are set under Additional Environment Variables.
- No harness cell pairs a node on custom ports.
- A node that starts while its home is unreachable falls back to 8081 for the home's live TV until its next start.
