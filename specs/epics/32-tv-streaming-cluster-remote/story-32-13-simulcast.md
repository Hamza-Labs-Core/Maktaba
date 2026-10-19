# Story 32.13 — Simulcast to YouTube, Facebook and other RTMP destinations

## Description

*As the admin, I want to send a Maktaba channel live to YouTube, Facebook or any RTMP/RTMPS
destination, either all the time or only while certain programmes air, without encoding it a
second time and without leaving my home's upload busy with one copy per platform.*

Cloudflare Stream live inputs can retransmit ("simulcast") whatever they receive to up to 50
outputs per input (`POST /accounts/{id}/stream/live_inputs/{input}/outputs` with `url` and
`streamKey`, optional `enabled`; [Simulcasting](https://developers.cloudflare.com/stream/stream-live/simulcasting/),
[API: create output](https://developers.cloudflare.com/api/resources/stream/subresources/live_inputs/subresources/outputs/methods/create/)).
Maktaba already pushes a channel once to a live input for remote viewers (32.7); this story adds
outputs to that input, so the home uploads the channel **once** whatever the number of
destinations, and Cloudflare fans it out. Simulcast output minutes are billed as **minutes
delivered** ($1 per 1,000; "Simulcasting via SRT or RTMP live outputs" counts as delivery —
[Pricing](https://developers.cloudflare.com/stream/pricing/)): one always-on output is ≈ 43,200
minutes ≈ **$43 per 30-day month**, so schedules and a separate cap matter.

Cloudflare forwards video and audio only. The event's title, description, privacy and thumbnail
are set on the platform (YouTube Studio, Facebook Live Producer) by the admin; creating or
updating YouTube/Facebook events through their APIs is out of scope (a possible later story).

Design: [design.md](design.md) §7.9; README R13, "Settings", "APIs", "Cost".

## Acceptance criteria

- **AC1 Destinations.** Admin → Live TV → **Simulcast** lists destinations: name, kind
  (`youtube` | `facebook` | `rtmp`), ingest URL (`rtmp://` or `rtmps://`; YouTube and Facebook
  prefill their documented ingest URL, editable), stream key. The key is stored in
  `home_secrets` (slot 0119, name `simulcast_<destination id>_key`), returned masked, never
  logged and never written to a table a node role can read. Edit, test ("URL parses, key set")
  and delete (refused while attached to a channel that is simulcasting).
- **AC2 Attachments.** On a channel's editor (Live TV page, channel row → Simulcast) the admin
  attaches destinations, at most `tv_simulcast.max_outputs_per_channel` (default 3; Cloudflare's
  limit is 50 per input), each with a mode:
  - `always` — the channel is pushed to Cloudflare continuously while the attachment is on,
    whether or not anyone watches remotely;
  - `schedule` — one or more rules: weekly time windows (days + start/end in the channel's time
    zone), "while programme X airs" (a video id), "while series Y airs" (a series id), or "during
    live slots" (`channel_programs.kind = live`). The controller resolves the rules against the
    channel's timeline and starts the output `lead_seconds` (default 60) before a matching block
    and stops it `tail_seconds` (default 30) after; adjacent matches merge into one window.
- **AC3 Content rights.** Turning an attachment on requires a confirmation dialog stating that
  YouTube/Facebook match uploaded and live content against copyright claims and that the admin
  holds the rights to everything the channel airs in that window; the confirmation is stored
  (`rights_confirmed_by`, `rights_confirmed_at`) and shown in the list. There is no "simulcast
  every channel" action and nothing is simulcast unless an admin attached it.
- **AC4 One push, many outputs.** A channel with an active simulcast window has a push
  (32.7) with `holds` containing `simulcast`; the push is not stopped by the idle timeout while
  any simulcast hold is active. Remote viewers of that channel share the same push. When the
  window ends and no remote viewer remains, the normal idle timeout applies.
- **AC5 Outputs follow the input.** Inputs are pooled (32.7), so outputs are created on the
  push's input when the window starts (`POST …/outputs` with `enabled: true`) and deleted when
  it ends (`DELETE …/outputs/{output}`; Cloudflare disconnects within 30 s). An idle input never
  keeps outputs, so a reassigned input can never send another channel to a destination.
- **AC6 Start/stop.** Each output has Start now / Stop now (admin), which override the schedule
  until the next window boundary; `always` attachments have an on/off toggle. Start/stop of one
  output never interrupts the channel's other outputs or remote viewers (outputs are enabled or
  deleted one by one).
- **AC7 Status.** Per output: `scheduled` (next window time), `starting`, `live`, `stopping`,
  `off`, `errored` (with Cloudflare's error code/message). The state comes from Maktaba's
  own record plus the input's status and lifecycle (polling, design §7.8) and, when configured,
  `live_input.errored` webhooks. Cloudflare documents no per-output health; whether the output
  object reports a connection status is verified in the 32.6 spike (README Q13) — until then a
  destination that rejects the stream shows "sent; the destination's state is shown on the
  platform".
- **AC8 Survives hot swaps and failovers.** A simulcast is one continuous push session:
  - the push runs in the home's front door push relay (design §7.4, Epic 33 story 33.8), which
    keeps one RTMPS/SRT session per channel and only switches its source on a block change,
    muxer restart, Epic 33 swap or rollback, or an Epic 32 failover/move: the live input never
    loses its publisher, so the outputs see no disconnect;
  - a relay ffmpeg crash restarts within 1 s on the same input (inside `timeoutSeconds`, same
    broadcast);
  - a rotation at `max_push_hours` (32.7 AC9) reuses the same input, so outputs persist; the
    destination sees a gap of at most a few seconds.
- **AC9 Limits and cost.** Simulcast minutes count towards `tv_remote.monthly_budget_usd`
  (delivered minutes × price, design §9) and also towards a separate cap,
  `tv_simulcast.max_hours_per_month` (default 60 output-hours). At 80 % of either: admin
  notification. At 100 % of the simulcast cap: no new output starts and running outputs stop at
  the end of their current window (`always` outputs stop at once, with a notification). At 100 %
  of the budget with `block_new`: no new output starts; running ones finish their window. A
  simulcast push holds one input (counts towards `max_channels`) and its recording counts
  towards the storage guard like any push.
- **AC10 Off by default.** `tv_simulcast.enabled = false`; simulcast needs the Cloudflare
  integration green (32.6) and is independent of `tv_remote.enabled` (a channel can be simulcast
  with remote viewing off).
- **AC11** en + ar strings for every label, the rights dialog and every error.

## Test cases

- **TC1** `TestDestinationKeyInHomeSecrets` — key sealed, masked in the API, `maktaba_worker`
  cannot read it.
- **TC2** `TestScheduleWindows` — weekly windows across midnight and DST; "series airs" on a
  fake timeline; adjacent matches merge; lead/tail.
- **TC3** `TestSimulcastHoldsPush` — idle timeout ignored while a hold is active; viewers share
  the push; the push stops after the window plus idle timeout.
- **TC4** `TestOutputsCreatedAndDeletedWithWindow` (fake Cloudflare) — `POST …/outputs` at window
  start with the key from `home_secrets`, `DELETE` at the end; none left on an idle input.
- **TC5** `TestOutputsSurviveFailoverAndRotation` — placement moves mid-window: same input, no
  output re-created; rotation: same input, outputs kept.
- **TC6** `TestSimulcastCapAndBudget` — 80 %, 100 % of the cap and of the budget; `always`
  stops at once at the cap, scheduled ones at window end.
- **TC7** `TestRightsConfirmationRequired` — enabling without confirmation → 409
  `rights_confirmation_required`.
- **TC8** Web: the Simulcast panel, the rights dialog, per-output status and start/stop.

## Edge cases

- **EC1** The destination rejects the key (YouTube "stream key invalid"): Cloudflare does not
  report it (AC7); the admin sees it on the platform. If the spike shows an output status, the
  output turns `errored` with it.
- **EC2** Two attachments of the same channel overlap: one push, two outputs.
- **EC3** A schedule window starts while the input pool is full: the output waits (`scheduled`,
  reason "no free input") and starts when an input frees; the admin is notified once.
- **EC4** The channel is disabled or deleted: its attachments stop; outputs are deleted.
- **EC5** The integration is removed (32.6 AC5): every output is deleted with the inputs.
- **EC6** YouTube ends an event after a long gap (its own timeout): a failover or rotation gap is
  seconds, below that; a host outage longer than the destination's timeout needs the admin to
  restart the event on the platform (runbook).

## Dependencies

32.6 (integration, `home_secrets`), 32.7 (inputs, push, holds), 32.10 (budget). Epic 33 story
33.8 (the push relay that keeps one session across swaps, AC8) — the requirement is stated here and checked in Epic 33's
tests.
