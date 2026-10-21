# Epic 33 — Maktaba TV staging, hot swap and change management

> **Status:** partly built (checked against the code 2026-10-19). **Built:** 33.1 two slots
> (Postgres home only), 33.2 releases, 33.3 generations, 33.4 splice-ready output, 33.7 hot swap,
> 33.9 rollback (no post-swap health trigger or crash-loop counter yet), 33.10 state machine, 33.11
> Changes page, 33.12 producers in review mode, 33.13 notifications and audit, 33.5 staging
> verification and 33.6 preview (shipped builds are built with
> `-tags tvstaging`: the Makefile's `STREAMING_TAGS`, the release workflow, the streaming
> Dockerfile; a build without the tag keeps the database-only checks). **Partly built:** 33.8 push
> continuity (no TC7 spike, `param_change_push=defer` or
> multi-host staging placements); 33.15 observability (swap,
> rollback, splice and stitch metrics only). **Not built:** 33.14 upgrade through a swap, 33.16
> docs and runbook. **Source:** `specs/epics/33-tv-staging-hot-swap/`.
> **Anchors:** [Epic 27 — Live Channels](../27-live-channels/README.md) (channel engine, scheduler,
> HDHomeRun, XMLTV/M3U), [Epic 32 — TV across the cluster and remote viewing](../32-tv-streaming-cluster-remote/README.md)
> (front door on the home, placement and fencing, failover, TS splice, Cloudflare push; branch
> `docs/epic-32-tv-streaming`, PR #60), [Epic 31 — Multi-instance](../31-multi-instance/README.md),
> [Epic 28 — Auto-update](../28-auto-update/README.md), [Epic 06 — Job queue](../06-job-queue/README.md)
> (the change-set seam: `engine/changes.py`, `write_mode` auto/review).
> **Builds on:** `feat/tv-core` (PR #63: `rules.tv_output` encoder settings, `channel_runtime` as a
> cluster record, `streaming_hosts`, slot 0117), `feat/plex-livetv` (PR #61, merged: the
> `plex_livetv` stage that pushes the channel map and reloads Plex's guide), `feat/process-watchdog`
> (in progress: the supervisor restarts crashed or hung children with backoff and liveness probes).

**Documents.** [design.md](design.md) (process topology, releases, schedule generations, warm-up and
verification, the swap in the front door, push continuity, rollback, the change state machine,
failure modes) · stories `story-33-01` … `story-33-16` · plans `plan-33-01` … `plan-33-16`.

## Goal

The owner's words: *"For TV we need a staging and hot swap feature, where changes happen to staging
and it is ready and live and then a switch happens — so two TV processes and a load-balanced hot
swap with an internal change management flow."*

Today every TV change lands on the air the moment it is written: `write_channels` rewrites
`channels`, a stale channel's future is regenerated in place after the block on air
(`channels/pass_.py`), a new `rules.tv_output` takes effect on the next block, and a new
`maktaba-streaming` binary means restarting the one process that encodes every channel. A bad change
is found by viewers.

This epic makes every TV change go through **staging**:

1. **TV slots per streaming host: `blue`, `green`, `amber`.** The slot that is **LIVE** serves
   viewers through the front door; a **hot STANDBY** runs the same live release on the home so a
   live crash fails over instantly; while a change is staged a third slot is **STAGING** and runs a
   *candidate release*: a new streaming binary, new encoder settings, a new lineup, new schedules,
   new art, new live slots — any combination.
2. **Staging is warmed and verified** on the channels people are watching right now plus probe
   channels: segments at the right wall-clock position, HLS and HDHomeRun TS health, the guide
   against the streams, Cloudflare ingest compatibility. An admin can watch staging at a staging URL.
3. **The front door switches** from LIVE to STAGING atomically per channel, at the next programme
   boundary (or at once): HLS viewers continue across one discontinuity at the same wall-clock
   position; Plex's HDHomeRun TS connections are spliced at a GOP boundary without dropping the
   HTTP connection; **the Cloudflare push (and every simulcast output hanging off it) keeps one
   unbroken session**. The old slot stays warm for a grace period — **rollback is the same switch,
   backwards** — then drains and stops.
4. **A change management flow**: every change is a *TV change* object (draft → staged → verifying →
   ready → live → superseded / rolled back) with a diff, checks, approval, a scheduled swap time,
   notifications and an audit trail, on a **Changes** page in the web app. `write_channels` and the
   channel editor produce TV changes instead of writing the air directly.

## Decisions

Owner decisions are marked **owner**; the others were accepted with the recommendation of the first
draft (**accepted default**). Full list under "Decisions on the open questions" at the end.

| Question | Decision |
|---|---|
| What is a slot | A separate OS process (`maktaba-streaming slot --slot blue|green|amber`), supervised by `maktaba-server`. Two engine instances in one process would share a crash and a binary, which defeats both purposes. Roles rotate between the three instances (`live`, `standby`, `staging`, `previous`). |
| Hot standby (**owner**) | On by default for the home: a second slot runs the **live release** and keeps the live channels warm (encoded twice). A live crash fails over to it instantly (same release, a splice). The standby **stays** while a change is staged; staging is a **third slot**, started only while a change is staged. All three respect tv-core's encoder caps (§ Resources). |
| Where the switch happens | In the **front door** (Epic 32 §4) — the stable edge process on the home that owns every client connection. Slots never own a client socket. |
| What a TV release is | An immutable record: streaming binary version + checksum, a snapshot of `rules.tv` / `rules.tv_output`, a lineup snapshot (every channel definition, art digests, filler references) and a **schedule generation** number. |
| How schedules are staged | `channel_programs` gains `sched_gen`; LIVE reads generation N, STAGING N+1; the swap flips `tv_routing.live_gen`. A schedule generation equals its predecessor up to each channel's next block boundary, so nothing is cut mid-programme. |
| When a channel switches | `swap_wait = boundary` (default): each watched channel switches at its next block boundary (at most `boundary_max_wait_minutes`, 30); unwatched channels switch at once. |
| Seamless splicing | Both slots stamp **wall-clock-anchored timestamps on a fixed GOP grid** (story 33.4), so the same instant has the same PTS and an IDR in both slots: the TS splice is exact and continuous. A re-timestamping splicer covers the cases where that does not hold. |
| Who owns the Cloudflare push | The **front door's push relay** (moves Epic 32 §7.4's pusher off the encoding slot). It holds one RTMPS/SRT session per channel and switches its *source* at a GOP boundary with continuous timestamps. Staging never pushes. |
| Rollback | Warm (instant, while the previous slot is kept warm, default 15 min) or cold (re-stage the previous release, minutes; retention 24 h — accepted default). A LIVE crash or crash loop inside the grace period rolls back automatically. |
| Approval (**owner**) | **Every change needs an admin's approval**: lineup, settings, schedule, art, live slots, upgrades. The auto-promote rules exist as settings but are all `approval` by default. Routine top-ups that only extend the existing grid with the same lineup and rules are **not** TV changes (they flow into every open generation); a *schedule change* is a change to grids, rules or content selection. **Amended by Epic 35 (owner, 2026-10-19):** a `binary` change made by the cluster updater (`source.kind = auto_update`) is promoted without approval (`auto_rule = immediate`, still at programme boundaries); every other kind, and a binary change made by hand, still needs approval. |
| Defaults (**owner**) | `tv_staging.enabled = true` by default once built (the owner's call overrides the usual "defaults = current behaviour"); the setting stays so it can be turned off (then: direct writes, one live slot plus the hot standby). Phases A–B ship behind a build flag until the epic is complete. |
| Swap timing (**owner**) | At each channel's programme boundary (`swap_wait = boundary`), as designed. |

## Scope

- Supervisor roles and process model for an edge (front door) and two slot processes per streaming
  host; slot health, ports, release binding.
- TV releases: config snapshot, lineup snapshot, binary reference; the `tv_releases` record.
- Schedule generations in `channel_programs` / `channel_schedule_state`; lineup lifecycle in
  `channels`; the top-up writing every open generation; garbage collection.
- Splice-ready encoder output: wall-clock PTS, fixed GOP grid, stable PIDs.
- Staging warm-up and automated verification; manual preview at staging URLs.
- The hot swap in the front door: routing epochs, HLS stitching, TS splicing, push-relay source
  switch; per channel at block boundaries or at once; single host or cluster-wide.
- Continuous Cloudflare push and simulcast outputs across swaps, rollbacks and slot failover.
- Rollback (warm, cold, automatic on crash).
- TV change objects, their state machine, approval, auto-promote rules, scheduling, cancel.
- A Changes page (diffs, checks, preview, approve, schedule, promote, rollback), en + ar.
- `write_channels`, the channel editor and settings saves producing TV changes; integration with
  `apply_or_review` and the queue's Apply.
- In-app notifications and audit log.
- Maktaba upgrades through the same swap.
- Metrics, events, runbook.

## Non-goals

- A second front door / two homes (Epic 31: one home). The front door's own restart is the one
  remaining TV interruption; it is kept small and rarely upgraded (design §13, decision 6).
- A staging HDHomeRun device for a test Plex (decision 10: browser preview only).
- Staging of on-demand video sessions (Epic 08). TV channels only.
- A/B testing (part of the viewers on staging, part on live). A swap is per channel and total.
- Multi-change merges: at most one change is in `staged…swapping` at a time; later drafts queue
  and rebase.
- Staging the pipeline, API or web roles. They are not viewer-facing; an upgrade restarts them
  normally after the TV swap (story 33.14).
- Re-encoding in the push relay to hide encoder-parameter changes from Cloudflare (decision 4).

---

## Requirements

### R1 — Two TV slots
- R1.1 Each streaming host runs one **front door** (on the home; on a node a thin slot host, see
  design §2) and up to three **slot processes**, `blue`, `green` and `amber`. Exactly one slot per
  host is `live`; the others are `staging`, `standby` (hot standby on the live release), `previous` (previous release kept warm for rollback), `draining` or `idle`.
- R1.2 A slot is bound to one TV release for its whole life. It reads the lineup, settings and
  schedule generation of that release, never "whatever is in `channels` now".
- R1.3 Slots listen on loopback (home) or on the Epic 32 signed streaming port (nodes); they
  never advertise SSDP and never accept a client connection directly.
- R1.4 A crash of the staging slot never affects the live slot (separate processes, separate output
  directories, separate encoder sessions). The supervisor restarts a crashed slot with backoff
  (process watchdog).
- R1.5 Hot standby (`hot_standby`, default **on** for the home): one slot runs the live release with
  the live slot's warm set (budget permitting, design §14) and takes over instantly when the live slot
  crashes; the crashed instance restarts as the new standby. The standby stays while a change is
  staged; the staging slot is the third instance, running only while a change is in
  `staged…swapping` or during the grace after a swap (as `previous`).
- R1.6 With `tv_staging.enabled = false` changes are written directly (today's behaviour) and only the
  live slot and the hot standby run.

### R2 — TV releases
- R2.1 A release is immutable once staged: `{id, kind, sched_gen, streaming_version,
  streaming_sha256, settings (rules.tv + rules.tv_output), lineup snapshot (each channel's full
  definition incl. number, slug, mode, mode_config, filler references, art digests), base_release_id}`.
- R2.2 Releases are content-addressed (digest of the snapshot); two equal releases are one.
- R2.3 The streaming binary of a release is installed side by side (`bin/releases/<version>/`) and
  verified against the release manifest's SHA-256 (Epic 28) before a slot may start it.
- R2.4 The previous live release and its schedule generation are retained for
  `rollback_retention_hours` (default 24) for cold rollback.

### R3 — Schedule generations
- R3.1 `channel_programs` and `channel_schedule_state` carry `sched_gen`. Every reader of the live
  guide or the live join reads `tv_live_gen()`; a slot reads its release's generation.
- R3.2 Staging a release that changes schedules creates generation N+1 by copying N up to each
  channel's divergence boundary and regenerating after it; channels whose schedule does not change
  copy N whole.
- R3.3 The divergence boundary of a channel is the end of its block on air at the planned swap
  time (re-anchored when the swap time moves), so the swap never cuts a programme.
- R3.4 The scheduler's top-up extends every open generation (live, staging, previous) until the
  older ones are superseded.
- R3.5 The swap flips `tv_routing.live_gen` in the same transaction that applies the lineup
  snapshot to `channels`; the guide (XMLTV, `/api/guide`) changes at that instant, and the
  `plex_livetv` stage is enqueued (channel map + guide reload).
- R3.6 New channels exist in `channels` with `lifecycle = staged` until the swap; removed channels
  become `retired` at the swap and are deleted after the retention period; number/slug uniqueness
  holds among `live` channels.

### R4 — Splice-ready output
- R4.1 Every station stamps PTS/DTS/PCR from wall-clock time (a fixed channel epoch), forces an IDR
  on a fixed wall-clock grid (`keyint_seconds`, default 2) and uses fixed PIDs, so two slots
  playing the same channel at the same instant produce the same timestamps and IDR instants.
- R4.2 HLS segments carry `EXT-X-PROGRAM-DATE-TIME` (already on) and start on grid IDRs.

### R5 — Warm-up and verification
- R5.1 Staging warms: every channel with demand on live (front-door clients attached, HLS touches
  within the warm grace, remote pushes), every changed channel for at least `verify_probe_seconds`
  (rolling, within the encoder budget), and the probe channel (`tv_staging.probe_channel`, default
  the lowest-numbered channel).
- R5.2 Warm-up never exceeds the staging encoder budget: `min(free hardware sessions, setting)`;
  channels beyond it are probed in turn and switched with a rolling swap (R6.6).
- R5.3 Automatic checks (design §6): slot health and version; per channel first segment within the
  ready timeout, segment cadence, PDT drift < 1 s, the block on air equals generation N+1 at now
  (and equals live's block for unchanged channels); TS conformity (PAT/PMT, PIDs, codec, profile,
  resolution, fps, IDR interval, closed GOP, B-frames, CC errors, PCR monotonic); HLS conformity;
  guide vs streams (XMLTV of N+1 covers the horizon with no gaps; lineup numbers unique; each
  channel has art); **Cloudflare ingest compatibility** for every channel (H.264, AAC or AC-3 with
  push audio transcode, closed GOP 2–10 s, combined bitrate 2–14 Mb/s; no B-frames when LL-HLS is
  on); **splice compatibility** with live per channel (seamless / param change); a soak of
  `verify_soak_seconds` (120) with no muxer restart.
- R5.4 A failed check fails the change (`failed`, with the report); warnings do not block but are
  shown; with an auto-promote rule switched on they block auto-promotion.
- R5.5 Warm-up and verification never push to Cloudflare and never touch a live input.
- R5.6 Staging's encoder budget comes after the live slot and the hot standby: it never takes
  sessions the live slot or the standby's warm set need (design §14).

### R6 — Hot swap
- R6.1 The front door holds `tv_routing` (live slot per host, live release, live generation,
  routing epoch). A swap writes the new routing under an advisory lock with `epoch + 1`, in one
  transaction with the lineup apply and the generation flip.
- R6.2 Per channel the switch is make-before-break: the front door already has the staging
  upstream open and aligned before it stops reading live.
- R6.3 **HLS:** the front door serves a stitched playlist with its own media sequence; the first
  staging segment whose `PROGRAM-DATE-TIME` starts at or after the end of the last live segment
  listed follows an `EXT-X-DISCONTINUITY`; `EXT-X-DISCONTINUITY-SEQUENCE` advances as live segments
  slide out. Viewers see at most one segment boundary and no rewind or skip larger than one GOP.
- R6.4 **HDHomeRun / `live.ts`:** each open TS connection is spliced at a grid IDR: live up to the
  IDR at instant t, staging from its IDR at t, continuity counters continued, PAT/PMT re-sent
  (version bumped if they differ); the HTTP connection is never closed. Tuner leases are untouched.
- R6.5 `swap_wait = boundary`: a watched channel switches at its next block boundary (≤
  `boundary_max_wait_minutes`, then at once); `immediate`: all channels at once.
- R6.6 Rolling swap when the encoder budget is short: a channel is warmed in staging shortly
  before its switch instant and stopped on live right after, keeping extra encodes ≤ the budget.
- R6.7 Cluster-wide (lineup, schedule or settings changes: all hosts at the same swap) or one host
  (binary-only releases for that host). The staging slot of a channel may sit on another host
  than its live slot (Epic 32 placement with `track = staging`); the front door switches upstream
  hosts the same way.
- R6.8 After the switch the old live slot becomes `previous` for `keep_warm_minutes` (15), keeping
  only the watched channels warm, then drains and stops; the old standby (old release) is drained at
  the switch and restarted on the new live release as the new hot standby once the grace ends (during
  the grace the `previous` slot is the crash target: R8.3).
- R6.9 Live-slot failover to the hot standby (outside a swap) uses the same per-channel splice, HLS
  stitch and relay source switch, at once (no boundary wait).

### R7 — Remote push and simulcast continuity
- R7.1 The Cloudflare push of a channel is owned by the front door's **push relay**, not by a slot
  or a host's engine: one long-lived ffmpeg per pushed channel (stream copy video, AAC audio)
  fed by the relay with the routed slot's TS.
- R7.2 At a swap, rollback or slot failover the relay switches its source at a grid IDR with
  continuous timestamps (exact with R4; re-timestamped otherwise) — Cloudflare sees one unbroken
  broadcast: same RTMPS/SRT session, same video id, tokens stay valid, no new recording, remote
  viewers do not re-tune.
- R7.3 **Simulcast outputs** (Epic 32 story 32.13: YouTube, Facebook, custom RTMP outputs attached to
  the live input) hang off that same input session and therefore continue without a disconnect;
  no simulcast output is touched by a swap. This single continuous publisher session is the only
  mechanism that protects them, so nothing in this epic may end or duplicate it.
- R7.3a Tested end to end: a fake RTMP/SRT ingest with an attached fake output asserts one continuous
  session on both, monotonic timestamps and no gap above one GOP across swap, rollback and slot failover
  (33.8 TC5, 33.9 TC6).
- R7.4 When a release changes video parameters (resolution, fps, profile, audio layout) and the
  in-session parameter change is not accepted by Cloudflare (spike, decision 4), the relay reconnects
  within 2 s — inside the input's `timeoutSeconds` (30), so the same video id continues — and the
  change is flagged in verification ("simulcast outputs may reconnect"); such a change needs an
  explicit acknowledgement in the approval, or waits for that channel's push to end
  (`param_change_push = defer`) — accepted default; the Cloudflare spike is tracked in 33.8.
- R7.5 Staging never pushes: the relay only subscribes to the routed slot; a staging slot has no
  push code path and no push credentials.
- R7.6 When the channel's host changes at the swap (multi-host), the relay on the home pulls the new
  host's TS (signed, Epic 32 §4.2) and switches source the same way; nothing reconnects.

### R8 — Rollback
- R8.1 Warm rollback: while the `previous` slot is up (grace), `POST /api/tv/rollback` swaps back with
  the same mechanism (stitch, splice, relay source switch), restores the previous lineup snapshot
  to `channels`, flips `live_gen` back, and re-runs `plex_livetv`.
- R8.2 Cold rollback: after the grace, rollback stages the previous release again (its generation
  is retained, re-anchored at the next boundary), verifies it, and swaps.
- R8.3 Automatic rollback: a live slot crash, crash loop (2 restarts in 10 min) or failed post-swap
  health check inside the grace rolls back warm and marks the change `rolled_back` (reason).
- R8.4 Rollback keeps R7 (no push or simulcast interruption).

### R9 — TV change objects
- R9.1 States: `draft → staged → verifying → ready → (approved) → swapping → live → superseded`;
  side exits `cancelled`, `rolled_back` (a failed verification returns the change to `draft`;
  `failed` is only the rows made before story 33.12 AC3). Transitions are recorded in
  `tv_change_events`.
- R9.2 Kinds (one change may carry several): `lineup`, `schedule`, `settings`, `art`, `live_slots`,
  `binary`. The diff lists channels added / removed / renumbered / renamed, changed definitions,
  per-channel schedule differences (first divergent block, programmes changed in the next 24 h),
  settings key diffs, binary versions.
- R9.3 Sources: a `write_channels` job, a channel editor save, a settings save in `tv`/`tv_output`,
  an upgrade, an admin "new change", a rollback.
- R9.4 At most one change in `staged…swapping`; newer drafts queue and rebase on the release that
  goes live (or are re-diffed against it).
- R9.5 Approval by an admin is required for **every** change kind (owner decision; optionally a second
  admin: `require_second_admin`). Auto-promote rules per kind (`approval` | `quiet_window` |
  `immediate`) exist as settings, all `approval` by default.
- R9.7 What is **not** a TV change: routine top-ups that only extend the existing grid with the same
  lineup, rules and content selection (they write every open generation, R3.4). A **schedule change**
  is a change to a channel's grid, rules (`rules.tv`, mode, slots) or content selection (sources,
  collections, filler lists), or a forced regeneration (`stale`).
- R9.6 A swap may be scheduled (`swap_at`); a ready change waits; the boundary planning uses that time.

### R10 — Changes page
- R10.1 Admin → Live TV → **Changes**: list (state, kinds, author/source, created, checks, swap time),
  detail (diff tabs: Lineup, Schedules, Settings, Binary & art; Checks; Preview; History),
  actions (Stage, Re-verify, Approve, Schedule, Swap now, Cancel, Roll back), slot status bar
  (live/standby/staging/previous per host, releases, encoder use).
- R10.2 English and Arabic, RTL layout.

### R11 — Producers in review mode
- R11.1 With `tv_staging.enabled`, `write_channels.apply_changes` writes a draft TV change
  (snapshot + change set) instead of `channels`; `write_mode = review` parks it as today and the
  Changes page's Approve is the queue's Apply (it posts the digest); `write_mode = auto` stages it
  right away (staged, verified, then waiting for approval under the default rules).
- R11.2 The channel editor and the filler/art editors save into the open change; the stale flag no
  longer rewrites the live generation. Every update joins the one open change until it is approved
  (story 33.12 AC3).
- R11.3 `plan_channels` stays a plan (not on air); its plan id is the change's provenance.
- R11.4 Top-ups (horizon extension) are not changes; they write every open generation.

### R12 — Notifications and audit
- R12.1 In-app notifications for: change ready (needs approval), failed, auto-promote scheduled
  (only when a rule is switched on), swapped, rolled back (manual/automatic), failover to the hot
  standby, staging slot crash, retention expiry of a rollback point. They use a new general
  `notifications` table (accepted default).
- R12.2 Every transition and admin action is written to `audit_log` (category `tv_change`) and
  `tv_change_events`.
- R12.3 A hook (`notify(kind, payload)`) for the general review/notification system to subscribe.

### R13 — Upgrades
- R13.0 *Amended by [Epic 35](../35-cluster-full-nodes-and-updates/README.md):* updates are fully automatic;
  the updater's `binary` change (`source.kind = auto_update`) does not wait for approval (R13.1 below
  applies to binary changes made by hand).
- R13.1 A Maktaba upgrade (Epic 28) installs side by side and creates a `binary` TV change, which is
  staged and verified and then waits for an admin's approval like every change; the TV swap happens
  first; the API and pipeline roles restart afterwards; the front door restarts only
  when its edge protocol version changes, in the quiet window.
- R13.2 A failed staging verification of the new version aborts the TV part of the upgrade and
  leaves the live slot on the old version.

### R14 — Observability
- R14.1 Metrics: slots by role/state per host, release per slot, changes by state, verification
  check results, swap duration and per-channel switch latency, splices (TS) and stitches (HLS),
  relay source switches and reconnects, rollbacks by reason, extra encodes during warm-up.
- R14.2 Events in the logs and the control centre: `tv_change_*`, `tv_slot_*`, `tv_swap_*`,
  `tv_rollback_*`, `tv_push_source_switch`.

### R15 — Docs
- R15.1 Runbook: turn staging on, read the Changes page, approve, schedule, roll back, what to do
  when verification fails, when a slot crash-loops, during upgrades; remote and simulcast notes.

## Acceptance (epic)

- With staging on, an admin renumbers channel 105 to 115 and adds a channel; a draft change shows
  both in the diff; Stage → Verify passes; the admin previews 115 in the browser at the staging URL.
- Approve with `swap_wait = boundary`: a browser watching 101 continues across the swap with one
  discontinuity at the end of the programme on air; Plex tuned to 101 keeps its connection (no
  re-tune, no "tuner unavailable"); after the swap Plex's guide shows 115 and the new channel.
- A phone watching 101 through Cloudflare sees no stall; the Cloudflare video id of the broadcast
  is unchanged; a YouTube simulcast output on that input never disconnects.
- Roll back within 15 minutes: everything above, reversed, with the same continuity.
- Killing the staging slot during verification does not affect any viewer; killing the live slot
  5 minutes after a swap rolls back automatically within 10 s, with viewers continuing.
- An upgrade to a new `maktaba-streaming` version swaps TV with no viewer interruption.
- With no change in flight, killing the live slot fails over to the hot standby within 2 s: browser
  and Plex viewers continue (one discontinuity / one splice), the Cloudflare broadcast and its simulcast
  outputs do not disconnect, and the crashed instance comes back as the new standby. An unapproved
  staged change is never put on the air by a crash.
- Every change — including a schedule-only change and an upgrade — waits for an admin's approval
  with the default settings; a routine top-up never creates a change.

---

## Architecture

```
                                   HOME (Mac Studio)
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │ maktaba-server (supervisor + process watchdog)                                          │
 │   ├─ api        /api/tv/changes, /api/tv/slots, /api/tv/rollback   change controller     │
 │   ├─ pipeline   write_channels → TV change (draft) · top-up writes every open sched_gen  │
 │   ├─ web        /stream gate (+ admin-only /stream/staging/…)                            │
 │   ├─ streaming  FRONT DOOR (edge, stable, small)                                         │
 │   │     SSDP · discover/lineup (from tv_routing.live) · XMLTV/M3U (live_gen)             │
 │   │     router: channel → (host, slot) by tv_routing epoch + placements (Epic 32)        │
 │   │     HLS stitcher ── TS splicer ── push relay (one ffmpeg per pushed channel)──RTMPS──┼──► Cloudflare
 │   │          ▲   ▲                       ▲                                               │    live input
 │   │          │   └──── staging upstream (warm, aligned, not routed) ─┐                   │    └─ simulcast
 │   │          └──────── live upstream ───────────────┐               │                   │       outputs
 │   ├─ tv-slot blue   release R_N   sched_gen N   LIVE     stations …  │                   │
 │   ├─ tv-slot green  release R_N   sched_gen N   STANDBY  (hot, same warm set, failover)  │
 │   └─ tv-slot amber  release R_N+1 sched_gen N+1 STAGING  warm set ───┘ (only while a     │
 │                                                          change is staged)               │
 │ Postgres: tv_releases, tv_release_channels, tv_changes, tv_change_events, tv_routing,     │
 │           tv_slots, channel_programs(sched_gen), channel_placements(track), notifications │
 └───────────────────────────────▲────────────────────────────────────────────────────────┘
                                 │ signed HTTP (Epic 32 §4.2), SQL (Epic 31 role)
 ┌───────────────────────────────┴───────── NODE (Windows PC, NVENC) ──────────────────────┐
 │ maktaba-server (node mode): tv-slot LIVE · STAGING (standby optional per host) — no edge  │
 └──────────────────────────────────────────────────────────────────────────────────────────┘
 Plex / Jellyfin / VLC / browsers talk to the front door only; Cloudflare gets one push per channel
 from the front door's relay, whichever slot or host encodes it.
```

## State machines

**TV change**

```
            stage (build release, gen N+1)        warm + checks           approve / auto rule
 draft ──────────────────────────────► staged ─────────► verifying ─────► ready ───────────────► approved
   │ cancel                              │ cancel           │ check failed    │ newer draft          │ swap_at reached
   ▼                                     ▼                  ▼                 ▼ (re-stage)            ▼
 cancelled                           cancelled            failed           staged               swapping ──► live
                                                                                                   │           │ next change live
                                                         rollback (manual / crash / health) ◄──────┴───────────┤
                                                                     ▼                                         ▼
                                                                rolled_back                                superseded
```

**Slot (per host)**

```
 idle ──start(release)──► starting ──healthy──► staging ──swap──► live ──next swap──► previous ──grace──► draining ──► idle
   │                          │ crash (watchdog restart)   ▲        ▲  │ crash outside grace: standby takes over
   │ start(live release)      └────────────────────────────┘        │  │ crash inside grace: auto rollback to previous
   └──────────────────────────► standby ──live crash (failover)─────┘  ▼
                                   │ swap: the old standby drains       crashed instance restarts as standby
                                   └────────► draining ──► idle ──► (after the grace) standby on the new release
```

Three instances (`blue`, `green`, `amber`) carry these roles; at most one each of `live`, `standby`,
`staging` and `previous` per host.

## Data model (migrations)

Slots as built: main ended at 0126 (Epic 32: 0123 TV cluster, 0124 canonical share paths, 0125
remote, 0126 simulcast) when 33.2/33.3/33.10/33.12 landed, so this epic's first two migrations are
0127 and 0128 (`SCHEMA_REV` 128). 0129 (runtime, 33.1/33.7) and 0130 (notifications, 33.13) have
landed too. `MANIFEST.md` gets rows only when the
files exist (its rule). Each has a `.sqlite.sql` sibling like every channel migration.

| Slot | Story | File | Tables / changes |
|---|---|---|---|
| 0127 | 33.2, 33.10 | `0127_tv_releases_changes.sql` (NO TRANSACTION, built) | `tv_releases` (id uuid, digest unique, kinds jsonb, sched_gen bigint unique, streaming_version, streaming_sha256, settings jsonb, lineup_digest, base_release_id, created_at, created_by); `tv_release_channels` (release_id, channel_id - no foreign key: a direct write with staging off may delete a channel an old release names - def jsonb, art jsonb, sched_changed bool, boundary_at = the divergence boundary, PK both); `tv_changes` (id, number - the "#42" of the UI, release_id, base_release_id, state, kinds jsonb, title, source jsonb, change_set jsonb, digest, diff jsonb (`diff.pending` = the producer's edits), verify_report jsonb, auto_rule, approved_by jsonb, approved_at, anchor_at = the planned swap instant the generation is built for, swap_at, swap_wait, created_by, created_at, updated_at, staged_at, ready_at, swapped_at, ended_at, end_reason, superseded_by); `tv_change_events` (id bigserial, change_id, at, actor_user_id, actor_source, from_state, to_state, action, detail jsonb); `tv_routing` (scope pk: `cluster` or `host:<id>`; live_release_id, live_gen, previous_release_id, previous_gen, epoch bigint, switched_at, previous_until); sequence `tv_sched_gen_seq` (SQLite: a table). Arrays are jsonb (not text[]/uuid[]) so the SQLite twin has the same shape. |
| 0128 | 33.3 | `0128_tv_sched_generations.sql` (NO TRANSACTION, built) | `channel_programs.sched_gen bigint not null default 0`; unique index `(channel_id, sched_gen, seq)` and lookup index `(channel_id, sched_gen, start_at, end_at)` built CONCURRENTLY, old `(channel_id, seq)` unique dropped; `channel_schedule_state` PK → `(channel_id, sched_gen)`; `channels.lifecycle text not null default 'live' check in ('staged','live','retired')`, `channels.retired_at`; number/slug unique indexes rebuilt as partial `WHERE lifecycle = 'live'`; function `tv_live_gen()` (stable, reads `tv_routing` cluster row, 0 when absent); view `channel_programs_live`; **breaking for older nodes**: `maktaba_min_node_schema()` returns 128. |
| 0129 | 33.1, 33.7 | `0129_tv_slots.sql` (built) | `tv_slots` (host_id, slot 'blue'/'green'/'amber', role, state, release_id, base_url, pid, version, warm jsonb, encoders_used int, started_at, beat_at, last_exit jsonb, PK (host_id, slot)); `channel_placements` (Epic 32 placements, now proposed 0121) gains `track text not null default 'live' check in ('live','staging','standby','previous')` and `slot text`, PK → `(channel_id, track)`; `tv_slot_beat(...)` SECURITY DEFINER own-row (Epic 31 guard); `tv_channel_switch` (change_id, channel_id, switch_at, state planned/switched/skipped, switched_at, PK both). |
| 0130 | 33.13 | `0130_notifications.sql` (built) | `notifications` (id, kind, severity, title_key, params jsonb, link, audience 'admins'/user_id, created_at, read_at per user via `notification_reads`; `notification_cursors`; `audit_log_category_check` gains `tv_change`, `tv_remote`) — the first user of a general in-app table (decision 8). |
| 0136 | 33.7, 33.9 | `0136_tv_swap_ops.sql` (built) | `tv_swap_ops` (id, change_id, kind swap/rollback, scope, release_id, from_release_id, epoch, reason, state planned/switching/done/failed/cancelled, committed, created_by, host_id, from_slot, to_slot, detail, created/started/done_at) and `tv_channel_switch` (op_id, change_id, channel_id, switch_at, state planned/switched/skipped, removed, switched_at, detail; PK op + channel) — in place of the design's `tv_channel_switch (change_id, channel_id)`: a change can be swapped and rolled back; `job_stages` `tv_rollback`. See "As built: 33.7, 33.8, 33.9". |

`channel_runtime` (0083/0117) gains nothing: a slot writes its rows with `host_id = <host>/<slot>`
so the On air page (Epic 32.5) shows both slots during a swap.

## Settings (`rules.tv_staging`; defaults are the owner's decisions)

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `true` | Changes go through staging (owner). Off: direct writes as today; the live slot and the hot standby still run. |
| `auto_promote` | `{lineup:"approval", settings:"approval", live_slots:"approval", art:"approval", schedule:"approval", binary:"approval", rollback:"approval"}` | Every kind needs approval (owner). Built as one setting per kind, `auto_promote_<kind>` (the settings page shows each with its choices). Strictest rule among a change's kinds wins. `approval` \| `quiet_window` \| `immediate`. A rollback pressed by an admin is itself the approval; the automatic rollback (`auto_rollback`) is not gated. |
| `quiet_window` | `"03:00-05:00"` | Local time window for front-door restarts (R13.1) and for any auto-promote rule switched on; auto-promotion also requires ≤ `quiet_max_viewers`. |
| `quiet_max_viewers` | `0` | |
| `swap_wait` | `boundary` | `boundary` \| `immediate`. |
| `boundary_max_wait_minutes` | `30` | A channel whose block runs longer switches at this deadline. |
| `keep_warm_minutes` | `15` | Old slot kept as `standby` (warm rollback). |
| `rollback_retention_hours` | `24` | Previous release + generation kept for cold rollback. |
| `verify_soak_seconds` | `120` | |
| `verify_probe_seconds` | `30` | Per changed channel that is not watched. |
| `probe_channel` | `""` | Slug; empty = lowest-numbered live channel. |
| `staging_max_extra_encodes` | `0` | 0 = auto (free hardware sessions on the host); otherwise a cap. |
| `rolling_swap` | `auto` | `auto` (when the budget is short) \| `always` \| `never`. |
| `auto_rollback` | `true` | Crash / crash loop / failed post-swap health in the grace. |
| `hot_standby` | `true` (home) | A slot runs the live release with the live slot's warm set (instant failover on a live crash; watched channels encoded twice). Nodes: `false` by default, per-host switch. |
| `standby_warm` | `demand` | `demand` (every channel with demand, as live) \| `viewers` (only channels with human viewers, then pushes ordered by remote viewers, when the encoder budget is short). Channels not warm on the standby fail over by restart (one stall; pushes reconnect inside 30 s). |
| `require_second_admin` | `false` | Approver must differ from the author. |
| `param_change_push` | `approve` | Video-parameter changes on pushed channels: `approve` (explicit ack) \| `defer` (switch that channel when its push ends). Accepted default. |

## APIs

```
# Changes (admin)
GET    /api/tv/changes?state=&limit=           list
POST   /api/tv/changes                         {title, from:"draft"} new empty draft (manual)
GET    /api/tv/changes/{id}                    change + diff + verify_report + events
PATCH  /api/tv/changes/{id}                    {title?, swap_at?, swap_wait?}
POST   /api/tv/changes/{id}/stage              build the release, generation N+1, start warm-up
POST   /api/tv/changes/{id}/verify             re-run verification
POST   /api/tv/changes/{id}/approve            {swap_at?, ack_param_change?}
POST   /api/tv/changes/{id}/promote            swap now (approved or rule allows)
POST   /api/tv/changes/{id}/cancel
POST   /api/tv/changes/{id}/preview            → {hls_base, m3u, xmltv, expires_at} (signed, 1 h)
POST   /api/tv/rollback                        {to_release_id?} warm if a previous slot is up, else cold
GET    /api/tv/slots                           per host: slots, roles, releases, warm sets, encoders
GET    /api/tv/releases?limit=                 releases with their changes

# Extended
GET    /api/tv/info                            + {staging:{enabled, live_release, change_in_flight}}
POST   /api/queues/write_channels/apply        unchanged; a TV change approve posts the same digest

# Front door internal (loopback on the home, never via the relay)
POST   {home-streaming}/internal/tv/routing/swap      {change_id, epoch}
POST   {home-streaming}/internal/tv/routing/rollback  {epoch}
GET    {home-streaming}/internal/tv/verify/{channel}  ts/hls probes of the staging upstream
# Slot internal (loopback / signed)
GET    {slot}/internal/tv/slot/status                 {release, sched_gen, version, stations[{id, block, pos, encoder}]}
POST   {slot}/internal/tv/slot/warm                   {channels:[…]}   (keep-alive hold)
POST   {slot}/internal/tv/slot/drain
GET    {slot}/stream/channel/{id}/…                   as today, slot-scoped dir
# Preview (admin, web port gate or signed ?pt=)
GET    /stream/staging/channel/{id}/index.m3u8 · /staging/lineup.m3u · /staging/xmltv.xml
```

## UI

Admin → Live TV gains **Changes** (story 33.11): a slot bar (per host: LIVE blue R12 · STANDBY green
R12 · STAGING amber R13 verifying 3/9 · encoders 14/16), the change list, and a detail view with tabs **Lineup** (added,
removed, renumbered, renamed, definition changes), **Schedules** (per channel: divergence at
`20:30`, next-24 h programmes before/after), **Settings** (key diff), **Binary & art**, **Checks**
(each check, per channel, pass/warn/fail, with the measured value), **Preview** (player on staging +
staging guide strip), **History** (events). Actions follow the state. A banner on Live TV pages
while a change is in flight; the channel editor shows "editing draft change #42".

## Security

- Approve, promote, rollback, cancel, preview: admin role; `require_second_admin` for four-eyes.
  Every action is in `audit_log` (append-only, slot 0059).
- Slots listen on loopback on the home; node slots accept only Epic 32 front-door-signed requests.
  Preview URLs are admin-gated on the web port or carry a short-lived HMAC (`pt`), never public.
- A slot starts only a binary from `bin/releases/<version>/` whose SHA-256 matches the release
  record and the Epic 28 manifest; the path is never taken from the API request.
- Staging slots never receive push credentials (`tv_push_credentials` answers the front door only).
- The swap is fenced: `tv_routing.epoch` under an advisory lock; slots refuse placements for a
  release that is not theirs; a stale front-door instance cannot route.

## Rollout

1. **Phase A — runtime** (33.1, 33.2, 33.3, 33.4): front door / slot split with the live slot and
   the hot standby; release records for what is live (release 1 = today); generations with only
   `sched_gen = 0`/`1`; aligned timestamps. Viewers see no change except instant failover.
2. **Phase B — staging and swap** (33.5–33.9): admin-triggered changes only (Changes page minimal),
   verification, swap, rollback, push continuity. Test with a test Plex and a test Cloudflare input.
   Staging is behind a build flag until Phase C.
3. **Phase C — change management** (33.10–33.13): producers in review mode, full Changes page,
   notifications; the build flag goes away and `tv_staging.enabled = true` (the default) takes effect.
4. **Phase D — upgrades** (33.14), observability (33.15), runbook (33.16) with each phase.

## As built: 33.2, 33.3, 33.10, 33.12 (before the two-slot runtime)

- **Default `tv_staging.enabled = true`** (the owner's "on once built"). It shipped off as an interim
  with these stories, because an approval was API-only, and flipped on with the Changes page (story
  33.11; pipeline `tvrelease/config.py`, API `tvchange.LoadConfig`, `schema.json`). See "As built:
  33.11, 33.13" below.

Stories 33.2 (releases), 33.3 (generations), 33.10 (TV changes) and 33.12 (producers) landed before
the runtime stories (33.1, 33.4, 33.7). What that means, and where the runtime hooks in:

- **One TV process, one live generation.** Every live reader (streaming `PGRepo`, HDHR lineup/guide,
  the API guide, channel list and schedule, placement, the remote-viewing schedule, the queue history)
  reads `channel_programs_live` / `lifecycle = 'live'`; with only generation 0 everything is as before.
  `streaming/internal/channel/repo_release.go` (`NewReleaseRepo`) is the release-bound repo a slot uses
  (story 33.2 AC3); `cmd/maktaba-server/internal/supervisor/release.go` (`ResolveReleaseBinary`) is the
  slot binary + checksum check (AC4); the supervisor's slot runner calls `ResolveReleaseBinary`
  (`supervisor/slots.go`).
- **Interim swap.** The pipeline stage `tv_swap` (`tvrelease/commit.py`) is the swap commit: it
  re-anchors the generation at the commit instant, then in one transaction applies the release's
  lineup (staged channels become live, removed ones retired), art, `rules.tv` / `rules.tv_output`,
  `write_channels`' links and filler pools, flips `tv_routing` (live/previous release and generation,
  `epoch + 1`, `previous_until`), marks the change `live` (the previous live change `superseded`),
  NOTIFYs `tv_routing` and enqueues `plex_livetv` (forced, reason `tv_swap`). Because N+1 equals N up to
  each changed channel's boundary, flipping the whole generation at the commit *is* a switch at each
  channel's programme boundary: the engine plays the programme on the air to its end and reads the new
  schedule after it. The lineup itself changes at the commit, as a direct write does today. Story 33.7
  keeps this commit (or moves it into `api/internal/tvchange/swap.go`) and moves the per-channel switch
  into the front door (`tv_channel_switch`); the controller's `startSwap` is the place that asks the
  front door instead of enqueuing `tv_swap`.
- **Interim verification.** Without a staging slot the controller (`api/internal/tvchange`) verifies
  on the database: lineup sanity (unique numbers and slugs, a logo), guide coverage of the new
  generation for the next 24 h without gaps, and the invariant that makes the swap cut-free - the block
  on the air now is the same in N+1 as in N (`channel.block`). `slot.health` is reported `skip`. Story
  33.5 adds the stream checks to the same report (`verify_report.checks`).
- **The flow.** A producer drafts (`tv_changes.diff.pending`): `write_channels` (review mode: a draft
  carrying the digest; auto mode or an approved digest: staged at once), the channel editor (every
  save: definitions, add, delete, renumber, strip, art, regenerate), TV settings saves (`rules.tv`,
  `rules.tv_output`), the scheduler's `stale` flag (a `schedule` draft). Stage (`POST .../stage`, the
  `tv_stage` job) builds the release and generation N+1; the controller verifies (`ready`); an admin
  approves (with `swap_at` or at once); at `swap_at` the controller moves it to `swapping` and enqueues
  `tv_swap`. Approving a reviewed `write_channels` draft posts the queue's Apply for its digest. With
  `tv_staging.enabled = false` every producer writes directly, as before.
- **Since built:** preview (33.6) and rollback (33.9) are implemented (`POST
  /api/tv/changes/{id}/preview`, `POST /api/tv/rollback`), `GET /api/tv/slots` reads `tv_slots`
  (33.1), change notifications (33.13) are sent, the Changes page (33.11) is built, and shipped
  builds include the staging slot (`-tags tvstaging`); the failover and crash-loop notices are
  sent from the slot events (`tvchange/slotfeed.go`).
- **Deviations from the text above.** `auto_promote` is one setting per kind (`auto_promote_<kind>`);
  array columns are jsonb; `tv_release_channels.channel_id` has no foreign key (EC1's `RESTRICT` would
  block today's direct deletes with staging off); `tv_release_channels.boundary_at`,
  `tv_changes.number` / `anchor_at` / `updated_at` were added; filler pools a staged `write_channels`
  change writes are applied at the swap (the new generation already plans from them, with the item ids
  the swap inserts); a re-anchor after a `swap_at` change happens at the commit; releases are not
  garbage-collected (a few rows each; generations, retired and staged channels are).

## Stories

| # | Story | Summary |
|---|---|---|
| 33.1 | [Two-slot TV runtime](story-33-01-two-slot-runtime.md) | Front door / slot split, `tv-slot` supervisor role (blue/green/amber), hot standby and instant failover, slot heartbeat, routing by slot. |
| 33.2 | [Releases and snapshots](story-33-02-releases-snapshots.md) | `tv_releases`, lineup + settings snapshot, side-by-side binaries, slot bound to a release. |
| 33.3 | [Schedule generations](story-33-03-schedule-generations.md) | `sched_gen`, copy-to-boundary, top-up of open generations, lineup lifecycle, GC. |
| 33.4 | [Splice-ready output](story-33-04-splice-ready-output.md) | Wall-clock PTS, fixed IDR grid, fixed PIDs. |
| 33.5 | [Staging warm-up and verification](story-33-05-staging-warmup-verification.md) | Warm set, budget, rolling probes, the checks, the report. |
| 33.6 | [Staging preview](story-33-06-staging-preview.md) | Admin staging URLs in the browser, player, staging guide (no staging HDHomeRun). |
| 33.7 | [Atomic hot swap in the front door](story-33-07-hot-swap-front-door.md) | Routing epochs, boundary planning, HLS stitch, TS splice, rolling swap, multi-host. |
| 33.8 | [Continuous remote push and simulcast](story-33-08-continuous-push-simulcast.md) | Push relay in the front door, source switch with continuous timestamps, outputs untouched. |
| 33.9 | [Rollback](story-33-09-rollback.md) | Warm, cold, automatic; symmetric continuity. |
| 33.10 | [TV change objects and state machine](story-33-10-tv-change-state-machine.md) | Change store, transitions, approval, auto-promote rules, scheduling, queueing. |
| 33.11 | [Changes page](story-33-11-changes-page.md) | Diff views, checks, preview, actions, slot bar; en + ar. |
| 33.12 | [Producers in review mode](story-33-12-producers-review-mode.md) | `write_channels`, editor and settings saves produce TV changes; queue Apply integration. |
| 33.13 | [Notifications and audit](story-33-13-notifications-audit.md) | In-app notifications, `audit_log`, hook. |
| 33.14 | [Upgrade via swap](story-33-14-upgrade-via-swap.md) | Epic 28 updates through a binary TV change; edge restarts only when needed. |
| 33.15 | [Observability](story-33-15-observability.md) | Metrics, events, control centre. |
| 33.16 | [Docs and runbook](story-33-16-docs-runbook.md) | Operating staging, swaps, rollback, failure playbook. |

## Dependencies

- `feat/tv-core` merged (output settings, NVENC/VideoToolbox caps, `streaming_hosts`, 0117).
- Epic 32 32.2 (placements, Epic 32 slot 0121) and 32.3 (front door package `streaming/internal/frontdoor`).
  33.1 can land first on a single host; whichever story lands first creates `frontdoor`.
  32.7's pusher moves into the front door's relay (33.8) — Epic 32 plan-32-07 should be amended
  accordingly if 33.8 lands first, or 33.8 moves it.
- `feat/process-watchdog` merged (per-child restart, backoff, probes): 33.1 adds the `tv-slot` role
  with three instances.
- Epic 32.7/32.8 (push, tokens) and 32.13 (simulcast outputs on the live input) for 33.8: the push
  relay is what keeps those outputs connected across swaps.
- Epic 28.3 (self-update) for 33.14.

## Resources and capacity

The home's encoder load in each phase (W = channels warm on live, i.e. every channel with demand; with
the planned **22 always-on channels pushed to Cloudflare for public/remote viewing**, W = 22):

| Phase | Slots running | Encodes |
|---|---|---|
| steady | live + hot standby | 2 × W |
| change staged (verifying / ready) | live + standby + staging | 2 × W + staging warm set (≤ `staging_max_extra_encodes`, rolling) |
| grace after a swap | new live + previous (the old standby drains at the switch) | 2 × W |
| after the grace | live + new standby | 2 × W |

tv-core caps a VideoToolbox host at 16 active channels (`EffectiveMaxActive`) and NVENC by its session
cap. With W = 22, **2 × W = 44 does not fit a 16 cap** (and 22 alone does not): live is placed first
(Epic 32 spills to nodes), the standby then warms only what the budget leaves (`standby_warm = viewers`:
channels with human viewers first, then pushes ordered by remote viewers), the remaining channels fail
over by restart (one stall; their pushes reconnect within 30 s, same video), and staging uses rolling
probes and a rolling swap. Live always gets its sessions first; staging never takes a session the live
slot or the standby's warm set needs.

Capacity work outside this epic: measure how many 1080p H.264 real-time encodes the M3 Ultra's
VideoToolbox sustains (the 16 cap is tv-core's conservative default; question 12), and add **Intel Quick
Sync (QSV)** as an encoder (`h264_qsv`) so a QSV machine can join as an Epic 32 streaming node for more
capacity (future).

## Decisions on the open questions

1. **Default — owner.** `tv_staging.enabled = true` by default once the epic is built; the setting stays
   to turn it off. This deliberately overrides the usual "defaults = current behaviour".
2. **Approval — owner.** Every change needs an admin's approval: lineup, settings, schedule, art, live
   slots, upgrades (a manual rollback is itself an admin action). The auto-promote rules stay as
   settings, all `approval`. Routine top-ups that only extend the existing grid with the same lineup,
   rules and content selection are **not** TV changes; a *schedule change* is a change to grids, rules
   or content selection (R9.7).
   *Amended by Epic 35 (owner, 2026-10-19):* the `binary` kind is exempt when the change comes from the
   cluster updater (`source.kind = auto_update`); all other kinds keep needing approval.
3. **Swap timing — owner.** At each channel's programme boundary (`swap_wait = boundary`), as designed.
4. **Encoder-parameter changes and Cloudflare — accepted default.** A resolution/fps change on a pushed
   channel needs explicit acknowledgement in the approval, or that channel waits for its push to end
   (`param_change_push = defer`). The Cloudflare spike (mid-session parameter change; simulcast output
   behaviour on a short reconnect) is tracked in 33.8.
5. **Push relay on the home — accepted default.** The push moves from the encoding host (Epic 32 §7.4)
   to the front door's relay; node-encoded pushed channels cross the LAN once more (≈ 6.5 Mb/s each).
6. **Front door upgrades — accepted default.** Rare, only when `edge_protocol` changes, in the quiet
   window; listener hand-off is a later improvement.
7. **Hot standby — owner.** Yes for the home: a second slot runs the live release (watched channels
   encoded twice); a live crash fails over instantly to it. The standby **stays** while a change is
   staged; staging is a **third slot**, warmed only while a change is staged; all within tv-core's
   encoder caps (see "Resources and capacity").
8. **Notifications table — accepted default.** Create the general `notifications` table (slot 0130).
9. **Retention — accepted default.** 24 h rollback retention.
10. **Preview — accepted default.** Browser preview only (plus the staging M3U/XMLTV URLs for VLC); no
    staging HDHomeRun device.
11. **Crash and unapproved changes — confirmed.** A live crash never promotes an unapproved staged
    change; it fails over to the hot standby running the live release (or, inside a swap's grace, rolls
    back to the `previous` slot).

## Open questions (remaining)

12. **VideoToolbox capacity.** How many simultaneous real-time encodes does the M3 Ultra sustain at the
    default output (1080p30, 6 Mb/s)? The answer decides whether 22 always-on channels fit with a full
    hot standby (44 encodes) or the standby warms only channels with viewers. A benchmark belongs to
    tv-core; QSV nodes are the future capacity path.

## As built: 33.11, 33.13 (Changes page, notifications)

- **Default on.** `rules.tv_staging.enabled` defaults to `true` again (pipeline `tvrelease/config.py`,
  API `tvchange.LoadConfig`, `schema.json`; the setting's help says so). Off: direct writes, as before.
- **Changes page** (`web/src/pages/Admin/LiveTV/Changes.tsx`, `/admin/live-tv/changes`): the slot
  bar (`GET /api/tv/slots`; one live process until 33.1, plus the change in flight as the staging
  slot), the in-flight banner, the list (state chip, kinds, source, author, checks summary, swap
  time) with fixed-order filters (all, drafts, in flight, live, ended), the staging settings
  (swap wait, quiet window, auto-promote rule per kind, second admin, rollback window; read from
  `GET /api/tv/changes` → `config`), and the release history. With staging off it explains that and
  keeps the release history (EC2). The Live TV page shows the change in flight and links here
  (`/api/tv/info` staging block gained `change_in_flight_number` / `_state`); the channel editor's
  "editing draft change #n" banner and the settings save notice link to the change.
- **Change detail** (`ChangeDetail.tsx`, `/admin/live-tv/changes/{id}`): tabs Lineup (added,
  removed, renumbered, renamed, definition fields; a draft shows `pending_summary`), Schedules
  (`GET /api/tv/changes/{id}/schedule-diff?page=` ten channels a page, `?channel=` the next 24 h of
  the live and the release generation from the divergence, "unchanged" folded), Settings (key diff
  labelled from the rules schema), Binary & art (version from → to, thumbnails before
  `/api/channels/{id}/art/{kind}` and after `?release=<id>`, admin-only), Checks
  (`verify_report.checks`, failures first), Preview (says story 33.6), History (events with actors
  and the change's `audit_log` rows). Actions are `allowed_actions` in a fixed order: Approve /
  Schedule / Swap now open one dialog (as soon as possible = each channel at its next programme
  boundary, or a set time; the swap mode; the expected switch time per channel from
  `GET /api/tv/changes/{id}/boundaries?at=&swap_wait=`, capped at `boundary_max_wait_minutes`; the
  param-change acknowledgement when `push.continuity` fails; the second-admin rule), Cancel confirms,
  Roll back is shown disabled with "arrives with story 33.9" (enabled by 33.9, below). Every change reply carries `users`
  (id → name), `audit` and `release`.
- **Live updates**: the page and the detail re-read every 2 s while visible; a poll that brings the
  same data keeps the same objects (`reconcile` / `reconcileList`), sections are always present in
  the same order (a quiet "no change in flight" line, a reserved status line), skeletons on the
  first load only (PR #76's rules). en + ar, RTL, phone width (the list becomes cards).
- **Notifications (33.13)**: slot **0130** `notifications` / `notification_reads` /
  `notification_cursors` (0129 stays with the runtime branch; `SCHEMA_REV` 130). `api/internal/notify`:
  `Notifier.Notify(kind, payload)` on a `Hub` whose first subscriber is the table (e-mail, push and
  the review flow subscribe later); a repeat of a kind about a subject is dropped inside its window
  (an hour; a day for "waiting" and the rollback point). `GET /api/notifications`,
  `POST /api/notifications/{id}/read`, `POST /api/notifications/read-all`; the bell in the web header
  (`NotificationsBell.tsx`) shows the unread count and the list, localised from `title_key` + params.
- **Where the notices come from**: rather than calls in each transition (the pipeline's `tv_stage` /
  `tv_swap` move states too), the controller tails `tv_change_events` (cursor in
  `notification_cursors`; a first run starts at the newest event): verified → `tv.change.ready`
  (needs approval) or `tv.change.auto_scheduled` (a rule switched on), failed → `tv.change.failed`,
  swap failed → `tv.change.swap_failed`, live → `tv.change.live`, rolled back →
  `tv.change.rolled_back` (cause, automatic or not); timers: ready for a day → `tv.change.waiting`,
  rollback point ending within the hour → `tv.rollback.expiring`. `tv.slot.failover` and
  `tv.slot.crash_loop` are defined for the runtime (33.1/33.9) to send through the same `Notifier`.
- **Audit**: the same feed writes an `audit_log` row (category `tv_change`, `actor_source` = the
  event's source, payload from / to) for every transition no admin made; admin actions keep their
  handler's row, now with from / to. Slot 0130 also widens `audit_log_category_check` with
  `tv_change` and `tv_remote`: on Postgres both categories were refused before (the 0054 check), so
  the 33.10 audit rows were never written there.

## As built: 33.5, 33.6 (staging verification and preview)

- **Who does what.** The API's change controller (`api/internal/tvchange/streamverify.go`,
  `runtime.go`) drives the runs; the home front door (`streaming/internal/frontdoor/staging.go`)
  runs the staging slot and samples it; the checks over bytes are
  `streaming/internal/frontdoor/verify` (TS packets, SPS, slice headers, ADTS/AC-3 parsed in Go, no
  ffprobe). The controller talks to the front door over three signed loopback routes:
  `POST /internal/tv/staging/start` (the plan: change, release, version + SHA-256, the release's
  channels, the changed and removed ones, the probe channel, `verify_probe_seconds`,
  `staging_max_extra_encodes`; idempotent, sent every tick so a front-door restart resumes),
  `POST /internal/tv/staging/stop`, `GET /internal/tv/staging` (slot, health, budget, warm set,
  rolling state, the newest sample per channel). A front door without slots (404) or without
  staging (the `tvstaging` build tag, `tv_staging.enabled`) keeps the database-only verification of
  33.10 (`slot.health` skip with the reason).
- **Start.** On `staged` the front door gives the third instance the `staging` role, writes
  `tv_slots.release_id`, and asks the supervisor's `POST /slots/{name}/start` with the release
  binding (`{release_id, version, sha256}`: the supervisor runs that version's binary and sets
  `MAKTABA_TV_RELEASE`). The change moves to `verifying` once the slot answers on that release and
  version (`slot.health`); not within 90 s → `failed (slot_not_healthy)`. A staging slot's fence
  airs its release's channels without a live placement (an idle channel, a channel the change adds);
  it never places one, never writes `channel_runtime`, and has no push path (TC6 checks the slot and
  engine packages import none).
- **Warm set and budget** (design §6.1, §14): demand on live, then preview viewers (33.6), the probe
  channel, then the changed channels not probed yet (rolling: each until it aired
  `verify_probe_seconds` and a sample measured min(probe, 10 s) of it); removed channels never.
  Budget = capacity − live's stations − the standby's warm set (NVENC / VideoToolbox: two sessions a
  channel), capped by `staging_max_extra_encodes`; the rest wait (`deferred`).
- **Sampling** (every 3 s, per warm channel): the staging slot's playlist and its newest ~10 s of
  segments, 3 s of its `live.ts` (the HDHomeRun path: PAT/PMT every ≤ 0.5 s, continuity, PCR), and —
  only when live airs the channel (asking live would start an encode) — the live slot's playlist and
  its two newest segments; the slots' status (the block on air, `reached`, encoder, restarts) is
  read before the samples. Slot status gained `channels[]` (story 33.5 step 1).
- **Checks** (per channel unless noted; `verify_report.checks`): `slot.health`,
  `channel.first_segment` (fail 75 s after the warm-up began), `channel.cadence`, `channel.wallclock`
  (newest segment end vs the time the playlist was read, < hls_time + 1 s), `channel.position`
  (output vs wall clock, < 2 s), `channel.block` (the slot airs generation N+1's block at its
  position; unchanged channels the same block as live), `ts.conformity` (PAT/PMT, PIDs, service id,
  H.264, resolution, fps, IDR interval and wall-clock grid, no B-frames, no open GOP, CC, PCR, audio
  codec — against the release's `tv_output` and the fixed layout), `hls.conformity` (program dates,
  independent segments, target duration, every segment starts with an IDR and carries PAT/PMT),
  `splice.align` (new: same PIDs as live, segment boundaries and first PTS within one frame —
  fail), `splice.compat` (`seamless` or `param_change: …`, warn), `cloudflare.ingest` (H.264,
  AAC/AC-3, GOP 2–10 s, 2–14 Mb/s, B-frames only with low latency; fail for a pushed channel, warn
  otherwise), `push.continuity` (pushed channels; a `param_change` fails until acknowledged at the
  approval, `param_change_push = defer` warns — the one fail that does not fail the change),
  `guide.vs_stream` (the guide's title of N+1 at the position = the stream's), `encoders` (no muxer
  restart or hardware fallback during the run, warn), `encoders.budget` (global: staging stations
  ≤ budget), plus the database checks (`lineup.sanity` — now also "a watched channel missing from the
  release" — `guide.coverage`, and the cut-free invariant renamed `schedule.boundary`).
- **Verdict.** A fail on two samples in a row (database checks: one) ends the run: `failed
  (verification_failed)` with `reasons`; else `ready` after `verify_soak_seconds` of samples with every
  warm channel measured and every changed channel probed (`soak_started_at`); a run that cannot finish
  (soak + probes + 3 × the first-segment timeout) fails `verification_timeout`; three staging-slot
  restarts during a run fail `slot_crash_loop`. `ready` re-checks health, wall clock, position and
  block every 60 s and returns to `verifying` on a fail (AC9). The report also carries `summary`
  (pass/warn/fail), `slot`, `warm`, `budget`, `status` (warming/soaking) and `run` (its state between
  ticks). The staging slot is stopped (supervisor stop, role idle, release binding cleared) whenever
  no change in `staged…swapping` names it — cancel, fail, live, rolled back — so story 33.7's switch
  finds it running through `approved`/`swapping` (`Slots.Switch` to the staging instance drops the
  staging state).
- **Preview (33.6).** `POST /api/tv/changes/{id}/preview` (admin; verifying/ready/approved) answers
  `{hls_base, token, m3u, xmltv, expires_at, channels[]}`: the token is
  `<change>.<exp>.<hmac>` with the cluster stream key over `preview\n<change>\n<exp>` (an hour;
  `cluster.PreviewToken` / `Keyring.CheckPreview`). The front door serves
  `/stream/staging/channel/{id}/index.m3u8?pt=` from the staging slot (segment URIs rewritten to
  `s/<slot>/seg-N.ts?pt=`), warming the channel as staging demand only (never live demand or a
  push; budget exhausted → 503 `Retry-After` + reason; a removed channel → live's, header
  `X-Maktaba-Preview: removed`), `/staging/lineup.m3u` and `/staging/xmltv.xml` from the release's
  lineup and generation (`hdhr.ReleaseGuide`). The token must name the staged change in a previewable
  state (else 403/410/503). The web port's stream gate lets `/stream/staging/channel/{uuid}/…` through
  for admin sessions only (`is_admin` of `/api/auth/me`, cached with the session). No staging
  HDHomeRun device and no SSDP change. The Changes page's Preview tab (`PreviewPanel.tsx`) mints and
  renews the token, plays a staged channel with the shared player (`@maktaba/tvplayer`: channel
  switching, digits), "Live vs staging" side by side, the staged guide strip (schedule diff of the
  channel) and the M3U / XMLTV links; en + ar.
- **Deviations.** `rules.tv_staging` defaults in the front door now match the pipeline and the API
  (`enabled = true`; still inert without the build tag). The verify report's per-channel stream
  checks come from the front door's evaluation (`verify.Evaluate`); the API adds what needs the
  database. `channel.BuildDefect` (`-ldflags -X …/channel.BuildDefect=pids|pts:<ms>`) builds a
  deliberately broken binary for the end-to-end test; release builds never set it.
- **Tested.** Unit: TS checks with crafted and ffmpeg-made streams (missing PAT, CC errors, wrong
  PIDs, PCR back, B-frames, wrong fps/resolution), Cloudflare rules, splice align/compat, playlist
  checks, warm set and budget, preview token, preview not live demand, budget exhausted, no staging
  device, the gate (admin only), the controller on a fake runtime (soak → ready, fail streak → failed
  + stop, wrong version → slot_not_healthy, no runtime → database checks, ready recheck). End to end
  (`api/internal/tvremote/staging_e2e_test.go`, real processes and ffmpeg, Postgres): front door +
  live + standby + staging; a good change is ready after the soak, previewed (staging HLS, M3U,
  XMLTV; live untouched), cancelled (the slot stops); a binary with other PIDs fails `ts.conformity`
  and `splice.align`, a binary 700 ms off the clock fails `splice.align`, a release missing a watched
  channel fails `lineup.sanity`.

## As built: 33.7, 33.8, 33.9 (the swap in the front door, push continuity, rollback)

- **The commit stays in the pipeline; the controller drives the front door through the
  database.** `tvrelease/commit.py` (the `tv_swap` job the controller's `startSwap` /
  `startSwapBy` enqueue) is still the one swap transaction (lineup, art, settings,
  bookkeeping, re-anchor, `tv_routing` flip with `epoch + 1` and `previous_until`, the
  previous live change superseded, `plex_livetv` forced). With TV slots (a live slot beat
  in the last 30 s) the same transaction writes the front door's swap operation: slot
  **0136** `tv_swap_ops` (kind `swap`, the release it airs and the one it replaces, the
  commit's epoch, `committed`) and one `tv_channel_switch` row per channel of both
  lineups with its planned instant (`swap_wait = boundary`: the end of the programme on
  the air in generation N, capped at `boundary_max_wait_minutes`; `immediate`, an added
  channel: the commit; a removed channel: its boundary, `removed`). The change stays
  `swapping` (event `swap_commit`); the controller (`api/internal/tvchange/swap.go`,
  `tickOps`) moves it to `live` (`swap_done`, with the counts and the slots) when the
  front door marks the operation `done`. Without slots (SQLite, `[tv] slots = off`) the
  commit is the whole swap, as before. Deviation from plan 33.7 (a POST to
  `/internal/tv/routing/swap` and a Go `swap.go` commit): the front door polls
  `tv_swap_ops` every second (it resumes a swap after its own restart; no signing, no
  URL), and the commit stays in Python, where the lineup apply and the re-anchor are.
  Slot roles are the front door's (it writes `tv_slots.role` under its role epoch);
  placements have no `track` (the home's slots share the channel's placement).
- **The front door** (`streaming/internal/frontdoor/swap.go`, `RunOps`): takes the
  operation, finds the target — the slot whose status reports the operation's release
  (the staging slot; when none runs it, it asks the supervisor to start one, unbound so
  it binds to the release the commit made live); a target that never answers fails the
  operation after 90 s (the controller rolls the commit back). A channel nobody watches
  switches at once; a watched one at its instant; with a short encoder budget the
  instants are spread `budget` at a time, 20 s apart, the target warms each channel only
  shortly before and the old live slot lets it go right after (rolling swap,
  `rolling_swap`). Per channel: `Route` (new connections), `Plan` (the splicers). Every
  instant passed with the target healthy → `switched`; down → tried once more 5 s later,
  then `skipped` (more than a quarter skipped: the controller rolls back). 8 s after the
  last instant the target is live, the old live slot `previous` until `previous_until`
  (warm on the watched channels), the old standby drained; at the end of the grace the
  previous slot drains and a standby starts on the live release. A routing switch during
  an operation answers 409 `swap_in_progress`. The staging slot (story 33.5's) is
  never a failover target; a change staged inside the grace never takes the previous slot.
- **HLS** (`Stitcher.Plan`): the live slot's segments are listed while they end by the
  instant; the target's segment that starts where the last one ended follows after one
  `EXT-X-DISCONTINUITY` (media sequence continuing, PDT continuous: 0 ms in the test).
  When a new source cuts its segments elsewhere (a station started off the grid) the
  segment holding that instant is taken (an overlap of less than a segment) instead of
  skipping one. A removed channel's playlist gets `EXT-X-ENDLIST` at its instant.
- **TS** (`tssplice.go`): each connection — Plex's tuners, `live.ts`, and the push relay —
  opens the target's TS 10 s before the instant, reads it alongside, and at the live
  slot's first IDR at or after the instant continues from the target's IDR with the same
  PTS (story 33.4 makes them equal): the target's PAT/PMT, then its packets; the retimer
  behind continues the continuity counters and finds no offset. When the live slot's IDR
  arrives first its packets wait up to 1 s for the target's; no match 6 s after the
  instant: the target joins at its newest IDR, retimed. The connection is never closed.
- **Push and simulcast (33.8)** are the TS path: the relay's source (`RelaySource`) is a
  front-door TS client, so a swap, rollback or failover is a source switch inside one
  Stream — one ffmpeg, one RTMPS session, the same Cloudflare video, the simulcast
  outputs untouched. Staging never pushes (the relay reads only the routed slot).
  Not built: the in-session parameter-change spike (TC7, manual) and the reconnect /
  `param_change_push = defer` handling of a resolution change on a pushed channel (the
  retimer still reconnects on a stream-layout change, as before); multi-host staging
  placements (`track = staging`): nodes run one engine and follow the live generation,
  which the commit flips (N+1 equals N before each boundary).
- **Release binding.** A slot started without `MAKTABA_TV_RELEASE` binds itself to the
  live release at its start, and the front door pins an unbound live slot or standby
  (`POST /internal/tv/slot/bind`), so the commit's generation flip never changes what the
  old slots air: the previous slot keeps release N for a warm rollback.
- **Rollback (33.9).** `GET /api/tv/rollback` is the rollback point (`warm` with the
  previous slot and `until`, `cold` until the retention, `none` and why);
  `POST /api/tv/rollback` (admin): warm — while the previous slot beats and
  `previous_until` is ahead (or the change is still swapping, EC1) — writes a `rollback`
  operation (every channel at once) and enqueues the reverse commit, the new
  `tv_rollback` job (`tvrelease/rollback.py`: the previous release's lineup back, the
  channels it added `staged` again, its art and settings, `tv_routing` back to the
  previous generation with `epoch + 1` and no rollback point left, the change
  `rolled_back` with `end_reason`, `plex_livetv`, and a draft "Re-stage #n" with its
  edits, AC6); a change in flight is cancelled first (EC2). Cold — after the grace, within
  `rollback_retention_hours` — creates a `rollback` change whose pending edits turn the
  live snapshot into the previous release's (every channel the live release regenerated
  planned anew from its boundary) and stages it; the request is its approval
  (`require_second_admin` still applies). Retention over: 409 `no_rollback_point`.
  Not reverted by the reverse commit: `write_channels`' links and filler pools.
- **Automatic rollback**: the front door, when the live slot is lost inside the grace
  (`auto_rollback`, on), routes every channel to the previous slot at once (20 ms after
  the kill in the test) and writes a `rollback` operation (`reason = auto:crash`); the
  controller enqueues its reverse commit. A slot lost during a swap: the old live slot →
  the remaining channels switch now; the target → the operation fails and the channels
  read the old live slot (the controller rolls the commit back). Outside the grace a crash
  fails over to the hot standby as before — never to staging. Not built: the post-swap
  health-check trigger (needs 33.5's checks) and a separate crash-loop counter (the first
  crash in the grace already rolls back).
- **Changes page**: Roll back is enabled; it says "Warm (instant) — 12 min left",
  "Cold (~3 min)" or why there is no rollback point (disabled), confirms, and reports the
  mode. en + ar.
- **Measured** (`api/internal/tvremote/swap_e2e_test.go`, `TestSwapRollbackEndToEnd`, real
  ffmpeg, three slots, one machine): swap — the TS splice at the boundary IDR with
  `late_ms=0`, the HLS discontinuity at the boundary with 0 ms PDT gap, the unwatched
  channel switched at once; warm rollback — the viewers back on the previous slot about
  1.5-2 s after the request (the next keyframe); automatic rollback — routed back 20 ms
  after the kill, HLS on the previous slot within 1 s; across two swaps, two rollbacks
  and a failover: one ingest session, largest video gap 34-375 ms, the simulcast output's
  one session never disconnected, TS one connection with frame-continuous timestamps.
  Metrics: `maktaba_tv_swaps_total`, `maktaba_tv_channel_switches_total`,
  `maktaba_tv_ts_splices_total{mode}`, `maktaba_tv_hls_stitches_total`,
  `maktaba_tv_rollbacks_total{reason}`, `maktaba_tv_channel_switch_latency_seconds`.
