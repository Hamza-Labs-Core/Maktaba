# Epic 33 — Maktaba TV staging, hot swap and change management

> **Status:** spec. **Source:** `specs/epics/33-tv-staging-hot-swap/`.
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

1. **Two TV slots per streaming host, blue and green.** The slot that is **LIVE** serves viewers
   through the front door; the other is **STAGING** and runs a *candidate release*: a new streaming
   binary, new encoder settings, a new lineup, new schedules, new art, new live slots — any
   combination.
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

## Decisions (recommended in this spec; see Open questions)

| Question | Recommendation |
|---|---|
| What is a slot | A separate OS process (`maktaba-streaming slot --slot blue|green`), supervised by `maktaba-server`. Two engine instances in one process would share a crash and a binary, which defeats both purposes. |
| Where the switch happens | In the **front door** (Epic 32 §4) — the stable edge process on the home that owns every client connection. Slots never own a client socket. |
| What a TV release is | An immutable record: streaming binary version + checksum, a snapshot of `rules.tv` / `rules.tv_output`, a lineup snapshot (every channel definition, art digests, filler references) and a **schedule generation** number. |
| How schedules are staged | `channel_programs` gains `sched_gen`; LIVE reads generation N, STAGING N+1; the swap flips `tv_routing.live_gen`. A schedule generation equals its predecessor up to each channel's next block boundary, so nothing is cut mid-programme. |
| When a channel switches | `swap_wait = boundary` (default): each watched channel switches at its next block boundary (at most `boundary_max_wait_minutes`, 30); unwatched channels switch at once. |
| Seamless splicing | Both slots stamp **wall-clock-anchored timestamps on a fixed GOP grid** (story 33.4), so the same instant has the same PTS and an IDR in both slots: the TS splice is exact and continuous. A re-timestamping splicer covers the cases where that does not hold. |
| Who owns the Cloudflare push | The **front door's push relay** (moves Epic 32 §7.4's pusher off the encoding slot). It holds one RTMPS/SRT session per channel and switches its *source* at a GOP boundary with continuous timestamps. Staging never pushes. |
| Rollback | Warm (instant, while the previous slot is kept warm, default 15 min) or cold (re-stage the previous release, minutes). A LIVE crash or crash loop inside the grace period rolls back automatically. |
| Approval | Admins approve. Per change kind: lineup and settings changes need approval; upgrades and schedule-only changes auto-promote in a quiet window once checks pass (settings). |
| Defaults | `tv_staging.enabled = false` ships dark (today's direct writes) — "rules as settings, defaults = current behaviour". Turning it on is the intended state (open question Q1). |

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
  remaining TV interruption; it is kept small and rarely upgraded (§ design 11, Q6).
- Staging of on-demand video sessions (Epic 08). TV channels only.
- A/B testing (part of the viewers on staging, part on live). A swap is per channel and total.
- Multi-change merges: at most one change is in `staged…swapping` at a time; later drafts queue
  and rebase.
- Staging the pipeline, API or web roles. They are not viewer-facing; an upgrade restarts them
  normally after the TV swap (story 33.14).
- Re-encoding in the push relay to hide encoder-parameter changes from Cloudflare (Q4).

---

## Requirements

### R1 — Two TV slots
- R1.1 Each streaming host runs one **front door** (on the home; on a node a thin slot host, see
  design §2) and up to two **slot processes**, `blue` and `green`. Exactly one slot per host is
  `live`; the other is `staging`, `standby` (previous release kept warm), `draining` or `idle`.
- R1.2 A slot is bound to one TV release for its whole life. It reads the lineup, settings and
  schedule generation of that release, never "whatever is in `channels` now".
- R1.3 Slots listen on loopback (home) or on the Epic 32 signed streaming port (nodes); they
  never advertise SSDP and never accept a client connection directly.
- R1.4 A crash of the staging slot never affects the live slot (separate processes, separate output
  directories, separate encoder sessions). The supervisor restarts a crashed slot with backoff
  (process watchdog).
- R1.5 Without `tv_staging.enabled` there is one slot (`blue`, live) and behaviour is today's.

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
- R3.4 The scheduler's top-up extends every open generation (live, staging, standby) until the
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
  shown, and block auto-promotion.
- R5.5 Warm-up and verification never push to Cloudflare and never touch a live input.

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
- R6.8 After the switch the old slot becomes `standby` for `keep_warm_minutes` (15), keeping only the
  watched channels warm, then drains and stops.

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
  in-session parameter change is not accepted by Cloudflare (spike, Q4), the relay reconnects
  within 2 s — inside the input's `timeoutSeconds` (30), so the same video id continues — and the
  change is flagged in verification ("simulcast outputs may reconnect"); such a change needs
  explicit approval and is never auto-promoted.
- R7.5 Staging never pushes: the relay only subscribes to the routed slot; a staging slot has no
  push code path and no push credentials.
- R7.6 When the channel's host changes at the swap (multi-host), the relay on the home pulls the new
  host's TS (signed, Epic 32 §4.2) and switches source the same way; nothing reconnects.

### R8 — Rollback
- R8.1 Warm rollback: while the previous slot is `standby`, `POST /api/tv/rollback` swaps back with
  the same mechanism (stitch, splice, relay source switch), restores the previous lineup snapshot
  to `channels`, flips `live_gen` back, and re-runs `plex_livetv`.
- R8.2 Cold rollback: after the grace, rollback stages the previous release again (its generation
  is retained, re-anchored at the next boundary), verifies it, and swaps.
- R8.3 Automatic rollback: a live slot crash, crash loop (2 restarts in 10 min) or failed post-swap
  health check inside the grace rolls back warm and marks the change `rolled_back` (reason).
- R8.4 Rollback keeps R7 (no push or simulcast interruption).

### R9 — TV change objects
- R9.1 States: `draft → staged → verifying → ready → (approved) → swapping → live → superseded`;
  side exits `failed`, `cancelled`, `rolled_back`. Transitions are recorded in `tv_change_events`.
- R9.2 Kinds (one change may carry several): `lineup`, `schedule`, `settings`, `art`, `live_slots`,
  `binary`. The diff lists channels added / removed / renumbered / renamed, changed definitions,
  per-channel schedule differences (first divergent block, programmes changed in the next 24 h),
  settings key diffs, binary versions.
- R9.3 Sources: a `write_channels` job, a channel editor save, a settings save in `tv`/`tv_output`,
  an upgrade, an admin "new change", a rollback.
- R9.4 At most one change in `staged…swapping`; newer drafts queue and rebase on the release that
  goes live (or are re-diffed against it).
- R9.5 Approval by an admin (optionally a second admin: `require_second_admin`). Auto-promote
  rules per kind: `approval` | `quiet_window` | `immediate`.
- R9.6 A swap may be scheduled (`swap_at`); a ready change waits; the boundary planning uses that time.

### R10 — Changes page
- R10.1 Admin → Live TV → **Changes**: list (state, kinds, author/source, created, checks, swap time),
  detail (diff tabs: Lineup, Schedules, Settings, Binary & art; Checks; Preview; History),
  actions (Stage, Re-verify, Approve, Schedule, Swap now, Cancel, Roll back), slot status bar
  (live/staging/standby per host, releases, encoder use).
- R10.2 English and Arabic, RTL layout.

### R11 — Producers in review mode
- R11.1 With `tv_staging.enabled`, `write_channels.apply_changes` writes a draft TV change
  (snapshot + change set) instead of `channels`; `write_mode = review` parks it as today and the
  Changes page's Approve is the queue's Apply (it posts the digest); `write_mode = auto` stages it
  right away and the auto-promote rule decides.
- R11.2 The channel editor and the filler/art editors save into the open draft; the stale flag no
  longer rewrites the live generation.
- R11.3 `plan_channels` stays a plan (not on air); its plan id is the change's provenance.
- R11.4 Top-ups (horizon extension) are not changes; they write every open generation.

### R12 — Notifications and audit
- R12.1 In-app notifications for: change ready (needs approval), failed, auto-promote scheduled,
  swapped, rolled back (manual/automatic), staging slot crash, retention expiry of a rollback point.
- R12.2 Every transition and admin action is written to `audit_log` (category `tv_change`) and
  `tv_change_events`.
- R12.3 A hook (`notify(kind, payload)`) for the general review/notification system to subscribe.

### R13 — Upgrades
- R13.1 A Maktaba upgrade (Epic 28) installs side by side and creates a `binary` TV change; the TV
  swap happens first; the API and pipeline roles restart afterwards; the front door restarts only
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
 │   ├─ tv-slot blue   release R_N   sched_gen N   LIVE    stations …   │                   │
 │   └─ tv-slot green  release R_N+1 sched_gen N+1 STAGING warm set ────┘                   │
 │ Postgres: tv_releases, tv_release_channels, tv_changes, tv_change_events, tv_routing,     │
 │           tv_slots, channel_programs(sched_gen), channel_placements(track), notifications │
 └───────────────────────────────▲────────────────────────────────────────────────────────┘
                                 │ signed HTTP (Epic 32 §4.2), SQL (Epic 31 role)
 ┌───────────────────────────────┴───────── NODE (Windows PC, NVENC) ──────────────────────┐
 │ maktaba-server (node mode): tv-slot blue (LIVE) · tv-slot green (STAGING)  — no edge      │
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
 idle ──start(release)──► starting ──healthy──► staging ──swap──► live ──next swap──► standby ──grace──► draining ──► idle
                              │ crash (watchdog restart)   ▲                │ crash: restart, or rollback in grace
                              └────────────────────────────┘                ▼
                                                                    (auto rollback ► standby becomes live)
```

## Data model (proposed migrations)

Slots after the highest on `main` (0118 `channel_edited_at`) and after Epic 32's reserved
0119–0121. Provisional; `MANIFEST.md` gets rows only when the files exist (its rule). Each has a
`.sqlite.sql` sibling like every channel migration.

| Slot | Story | File | Tables / changes |
|---|---|---|---|
| 0122 | 33.2, 33.10 | `0122_tv_releases_changes.sql` | `tv_releases` (id uuid, digest unique, kind text[], sched_gen bigint unique, streaming_version, streaming_sha256, settings jsonb, lineup_digest, base_release_id, created_at, created_by); `tv_release_channels` (release_id, channel_id, def jsonb, art jsonb, sched_changed bool, PK both); `tv_changes` (id, release_id, base_release_id, state, kinds text[], title, source jsonb, change_set jsonb, digest, diff jsonb, verify_report jsonb, auto_rule, approved_by uuid[], approved_at, swap_at, swap_wait, created_by, created_at, staged_at, ready_at, swapped_at, ended_at, end_reason, superseded_by); `tv_change_events` (id bigserial, change_id, at, actor_user_id, actor_source, from_state, to_state, action, detail jsonb); `tv_routing` (scope pk: `cluster` or `host:<id>`; live_release_id, live_gen, previous_release_id, previous_gen, epoch bigint, switched_at, standby_until); sequence `tv_sched_gen_seq` (starts at 1; generation 0 = everything today). |
| 0123 | 33.3 | `0123_tv_sched_generations.sql` (NO TRANSACTION) | `channel_programs.sched_gen bigint not null default 0`; unique index `(channel_id, sched_gen, seq)` and lookup index `(channel_id, sched_gen, start_at, end_at)` built CONCURRENTLY, old `(channel_id, seq)` unique dropped; `channel_schedule_state` PK → `(channel_id, sched_gen)`; `channels.lifecycle text not null default 'live' check in ('staged','live','retired')`, `channels.retired_at`; number/slug unique indexes rebuilt as partial `WHERE lifecycle = 'live'`; function `tv_live_gen()` (stable, reads `tv_routing` cluster row, 0 when absent); view `channel_programs_live`. |
| 0124 | 33.1, 33.7 | `0124_tv_slots.sql` | `tv_slots` (host_id, slot 'blue'/'green', role, state, release_id, base_url, pid, version, warm jsonb, encoders_used int, started_at, beat_at, last_exit jsonb, PK (host_id, slot)); `channel_placements` (slot 0120) gains `track text not null default 'live' check in ('live','staging','standby')` and `slot text`, PK → `(channel_id, track)`; `tv_slot_beat(...)` SECURITY DEFINER own-row (Epic 31 guard); `tv_channel_switch` (change_id, channel_id, switch_at, state planned/switched/skipped, switched_at, PK both). |
| 0125 | 33.13 | `0125_notifications.sql` | `notifications` (id, kind, severity, title_key, params jsonb, link, audience 'admins'/user_id, created_at, read_at per user via `notification_reads`) — the first user of a general in-app table (Q8). |

`channel_runtime` (0083/0117) gains nothing: a slot writes its rows with `host_id = <host>/<slot>`
so the On air page (Epic 32.5) shows both slots during a swap.

## Settings (`rules.tv_staging`; defaults keep today's behaviour)

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Changes go through staging. Off: direct writes, one slot (today). |
| `auto_promote` | `{lineup:"approval", settings:"approval", live_slots:"approval", art:"quiet_window", schedule:"quiet_window", binary:"quiet_window", rollback:"immediate"}` | Strictest rule among a change's kinds wins. `approval` \| `quiet_window` \| `immediate`. |
| `quiet_window` | `"03:00-05:00"` | Local time window for auto-promotion; also requires ≤ `quiet_max_viewers` (0 = no one watching). |
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
| `hot_standby` | `false` | Keep the second slot running the live release when no change is staged (instant failover on a live crash; doubles encodes of watched channels). |
| `require_second_admin` | `false` | Approver must differ from the author. |
| `preview_hdhr` | `false` | Also expose staging as a second HDHomeRun device (for a test Plex). |
| `param_change_push` | `approve` | Video-parameter changes on pushed channels: `approve` (explicit ack) \| `defer` (switch that channel when its push ends). |

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
POST   /api/tv/rollback                        {to_release_id?} warm if standby, else cold
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

Admin → Live TV gains **Changes** (story 33.11): a slot bar (per host: LIVE blue R12 · STAGING green
R13 verifying 3/9 · encoders 6/16), the change list, and a detail view with tabs **Lineup** (added,
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

1. **Phase A — runtime** (33.1, 33.2, 33.3, 33.4): front door / slot split with one slot; release
   records for what is live (release 1 = today); generations with only `sched_gen = 0`/`1`;
   aligned timestamps. Ships dark: viewers see no change.
2. **Phase B — staging and swap** (33.5–33.9): admin-triggered changes only (Changes page minimal),
   verification, swap, rollback, push continuity. Test with a test Plex and a test Cloudflare input.
3. **Phase C — change management** (33.10–33.13): producers in review mode, full Changes page,
   notifications; flip `tv_staging.enabled` on the home.
4. **Phase D — upgrades** (33.14), observability (33.15), runbook (33.16) with each phase.

## Stories

| # | Story | Summary |
|---|---|---|
| 33.1 | [Two-slot TV runtime](story-33-01-two-slot-runtime.md) | Front door / slot split, `tv-slot` supervisor role, blue/green, slot heartbeat, routing by slot. |
| 33.2 | [Releases and snapshots](story-33-02-releases-snapshots.md) | `tv_releases`, lineup + settings snapshot, side-by-side binaries, slot bound to a release. |
| 33.3 | [Schedule generations](story-33-03-schedule-generations.md) | `sched_gen`, copy-to-boundary, top-up of open generations, lineup lifecycle, GC. |
| 33.4 | [Splice-ready output](story-33-04-splice-ready-output.md) | Wall-clock PTS, fixed IDR grid, fixed PIDs. |
| 33.5 | [Staging warm-up and verification](story-33-05-staging-warmup-verification.md) | Warm set, budget, rolling probes, the checks, the report. |
| 33.6 | [Staging preview](story-33-06-staging-preview.md) | Admin staging URLs, player, staging guide, optional staging HDHomeRun. |
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
- Epic 32 32.2 (placements, slot 0120) and 32.3 (front door package `streaming/internal/frontdoor`).
  33.1 can land first on a single host; whichever story lands first creates `frontdoor`.
  32.7's pusher moves into the front door's relay (33.8) — Epic 32 plan-32-07 should be amended
  accordingly if 33.8 lands first, or 33.8 moves it.
- `feat/process-watchdog` merged (per-child restart, backoff, probes): 33.1 adds the `tv-slot` role
  with two instances.
- Epic 32.7/32.8 (push, tokens) and 32.13 (simulcast outputs on the live input) for 33.8: the push
  relay is what keeps those outputs connected across swaps.
- Epic 28.3 (self-update) for 33.14.

## Open questions (with the recommendation used in this spec)

1. **Default on or off?** Rules-as-settings says defaults keep today's behaviour, so
   `tv_staging.enabled = false` ships dark. *Recommendation:* turn it on for the home after
   Phase B and make it the default in a later release. Confirm.
2. **Approval policy.** Lineup and settings changes need an admin; schedule-only, art and upgrades
   auto-promote in the quiet window (03:00–05:00, nobody watching). Is that the right split? Should
   a `write_channels` run from a nightly pipeline ever auto-promote a lineup change?
3. **Swap at the programme boundary** (default) means a change waits up to 30 min per watched
   channel. Or always swap at once (one mid-programme splice, invisible with aligned output)?
4. **Encoder-parameter changes and Cloudflare.** Whether Cloudflare accepts a mid-session H.264
   parameter change (new SPS/resolution) on RTMPS/SRT is not documented: spike in 33.8. If not, a
   resolution/fps change on a pushed channel reconnects inside `timeoutSeconds` (same video), but
   YouTube/Facebook simulcast outputs may still see a reconnect. *Recommendation:* such changes need
   explicit approval (`param_change_push = approve`), or wait for the push to end (`defer`).
5. **Push relay on the home.** Moving the push from the encoding host (Epic 32 §7.4) to the
   front door makes node-encoded channels cross the LAN once more (≈ 6.5 Mb/s per pushed channel)
   but is what makes a host change invisible to Cloudflare. Accept?
6. **The front door's own upgrades.** It is the one process whose restart interrupts TV (Plex
   reconnects, HLS retries, pushes reconnect within 30 s — same video). Keep it rare and in the quiet
   window (recommended), or invest in listener hand-off (pass sockets to a new edge process) later?
7. **Hot standby** (`hot_standby`): run the live release in the second slot when nothing is
   staged, for instant failover on a live crash, at the cost of double encodes of watched channels.
   Off by default; on for the home?
8. **Notifications table.** There is no general in-app notifications store (only mobile push
   categories, 0065). Create `notifications` here as the general one (recommended), or wait?
9. **Retention.** 24 h of rollback point (old generation and lineup snapshot) — enough?
10. **Staging HDHomeRun for a test Plex** (`preview_hdhr`): useful, or browser preview only?
11. **Failover to an unapproved staging slot.** On a live crash the spec restarts live (or rolls back
    in the grace) and never promotes an unapproved change; staging is used for failover only when its
    change is approved and ready ("early promotion"). Confirm.
