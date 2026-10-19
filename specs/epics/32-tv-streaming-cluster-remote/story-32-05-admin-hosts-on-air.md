# Story 32.5 — Admin: streaming hosts and on air

## Description

*As the admin, I want to see which machines stream TV, how full they are, which channels are on
air where and with what encoder, and switch machines on, drain them or move a channel — from the
Live TV page.*

This also delivers the audit's P1-5 ("On air now" from `channel_runtime`), now cluster-wide.

Design: [design.md](design.md) §2–§3, §6.3.

## Acceptance criteria

- **AC1** `GET /api/tv/cluster` (admin) returns the cluster settings, every host
  (`streaming_hosts` + health: online/down/draining/disabled/unfit with reason, encoders,
  capacity, used, load, last beat, version, can_push, can_live) and every placement (channel, host,
  generation, state, reason, since) joined with `channel_runtime` (encoder, viewers, restarts,
  NVENC fallbacks) and the remote push state when there is one.
- **AC2** Admin → Live TV → **Streaming hosts**: one card per host with the "Stream on this
  machine" switch (disabled for the home with the reason "the home always streams"), capacity
  override, weight, stream base URL (editable for nodes), drain/undrain, and why it is not taking
  channels. The cluster switch `tv_cluster.nodes_stream`, the policy and `spill_at` are on the same
  section (the settings page also shows them).
- **AC3** **On air**: a table of channels on air — number, name, host, encoder, HLS viewers,
  tuners, remote viewers, since, restarts, NVENC fallbacks, push state — with Move (host picker or
  "automatic") and Stop, each asking for confirmation that viewers will be interrupted.
- **AC4** Refresh every 5 s while visible; no refresh when hidden.
- **AC5** Pins: a per-channel "Prefer host" select on the channel edit page writes
  `tv_cluster.pins`.
- **AC6** English and Arabic strings; RTL layout; the page works at phone width.
- **AC7** Stale `channel_runtime` rows (no matching placement, host down) are shown as "stale" and
  cleaned by the home at boot and every minute.

## Test cases

- **TC1** API: `TestClusterGetShape`, `TestPutHostValidation` (home cannot be disabled; capacity
  override 0–64; URL must be http(s)).
- **TC2** Web (vitest + testing-library): host switch, drain, move dialog, stop confirm, Arabic render.
- **TC3** `TestStaleRuntimeCleanup`.

## Edge cases

- **EC1** A node that never beat since joining: listed under the Epic 31 Machines page only, not here.
- **EC2** Two `home` rows (misconfiguration): a red banner with both host names.

## Dependencies

32.1–32.4. Shares the Live TV admin page with Epic 27 (`web/src/pages/Admin/LiveTV.tsx`).
