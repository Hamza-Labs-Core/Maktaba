# Design — Maktaba TV staging, hot swap and change management (Epic 33)

Requirements: [README.md](README.md). Stories and plans: `story-33-*`, `plan-33-*`.
Terms from Epic 32 are kept: **front door** (the home's streaming process that owns every client
connection), **streaming host**, **placement** (`channel_placements`, fenced by the *placement
generation*). This epic adds **slot**, **release**, **schedule generation** (`sched_gen` — not to be
confused with the placement generation), **routing epoch** and **TV change**.

## 1. Shape

| Piece | Where | Built on |
|---|---|---|
| **Front door** (edge) | the home's `streaming` role (`maktaba-streaming serve`) | Epic 32 `streaming/internal/frontdoor`; `hdhr` handler, SSDP, XMLTV/M3U; the web port's `/stream` gate (`cmd/maktaba-server/stream_gate.go`) |
| **Slots** `blue`, `green`, `amber` | new supervisor role `tv-slot` (`maktaba-streaming slot --slot blue`), up to three instances per streaming host: live, hot standby, staging (§2.4) | `streaming/internal/channel` (Engine, Station, Hub, muxer, HLS dir), unchanged in substance |
| **Change controller** | the home API, new package `api/internal/tvchange` | `tv_changes`, `tv_releases`, rules settings, `audit_log` |
| **Release builder + generations** | pipeline (`pipeline/src/maktaba_pipeline/tvrelease/`) and the scheduler pass | `engine/changes.py`, `engine/stages/write_channels.py`, `channels/pass_.py`, `channels/repo.py` |
| **Push relay** | inside the front door, `streaming/internal/frontdoor/relay` | Epic 32 §7.4 pusher, moved |

Today the streaming process does everything: SSDP, HDHR, XMLTV, the channel engine and its ffmpeg
children, HLS directories, on-demand video sessions. After this epic the process that **owns client
sockets** (front door) is separated from the processes that **encode** (slots). Viewers connect
only to the front door; the front door reads from a slot. Replacing what encodes then never closes a
client socket.

```
client ─► front door ─ route(channel) ─► (host, slot) by tv_routing.epoch + placement(track=live)
                 │                         ├─ HLS: stitched playlist, segments proxied per slot
                 │                         ├─ TS : splicing copier (one upstream at a time)
                 │                         └─ push relay: one ffmpeg → Cloudflare live input
                 └─ during a swap: also opens (host, slot) of placement(track=staging), aligns, switches
```

On-demand video sessions (Epic 08) stay in the front door process (they are not TV and have their
own lifecycle); only the channel engine moves into slots.

## 2. Slots

### 2.1 Process model

`maktaba-server serve` (supervisor) starts roles `api`, `pipeline`, `web`, `streaming` (front door)
and the slot instances the slot roles need: the live slot and, with `hot_standby` (default on for the
home), the standby — two instances in steady state; the third instance only while a change is staged
or during a swap's grace (§2.4). Roles rotate between `blue`, `green` and `amber`; the instance name
carries no meaning. With `tv_staging.enabled = false` no staging slot is ever started (changes are
written directly). Each slot:

| Item | Value |
|---|---|
| Command | `bin/releases/<version>/maktaba-streaming slot --slot blue` (version from the slot's release; the supervisor resolves it from `tv_slots.release_id` at each (re)start) |
| Listen | `127.0.0.1:8791` (blue) / `8792` (green) / `8793` (amber) on the home; on a node the Epic 32 streaming port + `/slot/<name>` prefix behind the signature check |
| Admin | `127.0.0.1:9111` / `9112` / `9113` — `/healthz`, `/readyz` for the watchdog probe |
| Output dir | `<cache>/tv/<slot>/<channel>/` (today `<cache>/tv/<channel>/`); no two slots share files |
| Env | `MAKTABA_TV_SLOT=blue`, `MAKTABA_TV_RELEASE=<uuid>`, `DATABASE_URL`, output settings come from the release |
| Encoders | the host's hardware sessions are shared; each slot reports its use; the host budget is enforced by the front door/placement (§14) |

A node (Epic 32) runs slots in node mode without a front door of its own: the home's front door
addresses `(node, slot)` directly over the signed streaming port (Epic 32 §4.4 gains the slot
prefix). A node therefore needs no edge, and every client-facing and Cloudflare-facing socket stays
on the home.

### 2.2 Slot record and heartbeat

```sql
tv_slots(host_id text, slot text check (slot in ('blue','green','amber')),
         role  text check (role in ('idle','staging','live','standby','previous','draining')),
         state text check (state in ('stopped','starting','healthy','unhealthy','crashed')),
         release_id uuid, base_url text, pid int, version text,
         warm jsonb,           -- channels the slot holds warm and why (demand|probe|rolling)
         encoders_used int, started_at timestamptz, beat_at timestamptz, last_exit jsonb,
         primary key (host_id, slot))
```

Every 5 s a slot calls `tv_slot_beat(host_id, slot, info)` (SECURITY DEFINER, own row; on a node
the Epic 31 `session_user` guard). `role` is written only by the change controller / front door
(the slot reads it), `state` only by the slot and the supervisor. A slot whose `release_id` does not
match the release it was started with refuses to run stations (`unhealthy: release_mismatch`).

### 2.3 Engine in a slot

The slot's `Engine` is today's engine with three differences:

1. **Repo bound to the release.** `repo_pg.go` reads channel definitions from
   `tv_release_channels(release_id)` and programmes `WHERE sched_gen = <release.sched_gen>`;
   settings from `tv_releases.settings` (not `app_settings`).
2. **Placement fence with track.** It starts a station only when a placement names this host, this
   slot and this track (Epic 32 §3.4 fence + `track`, `slot`).
3. **Holds.** `POST /internal/tv/slot/warm {channels}` attaches a synthetic viewer per channel
   (`reg.attach`), refreshed every 10 s by the front door, so the reaper keeps warmed channels up.

### 2.4 Hot standby and failover (owner decision)

The **standby** slot runs the live release and the same `sched_gen`; the front door keeps the
standby's warm set equal to live's (`standby_warm = demand`) or, when the encoder budget is short,
to the channels with human viewers and then the pushes with the most remote viewers
(`standby_warm = viewers`, §14). Its stations are frame-aligned with live's (§5), so a failover is an
exact splice.

```
live slot crash / hang (watchdog probe, upstream EOF, 3 proxy errors)
  └─► front door: epoch + 1, standby → live for every channel at once (no boundary wait):
        HLS stitch, TS splice, relay source switch — same mechanisms as a swap (§8.4, §8.5, §9)
        channels not warm on the standby: started on the new live slot (one stall; pushes
        reconnect inside timeoutSeconds, same video id)
  └─► supervisor restarts the crashed instance; it comes back as the new standby and warms
```

Role layout over a change's life (one host):

| Moment | live | standby | staging | previous |
|---|---|---|---|---|
| steady | blue (R_N) | green (R_N) | — | — |
| change staged / verifying / ready | blue (R_N) | green (R_N) | amber (R_N+1) | — |
| swap commit → grace | amber (R_N+1) | — (green drains at the switch) | — | blue (R_N) |
| after the grace | amber (R_N+1) | green restarted on R_N+1 | — | — (blue stops) |

During the grace the `previous` slot is the crash target (automatic rollback, §10), which is why the
new release's standby only starts after the grace: it keeps the peak at three slots and the encodes
at 2 × W (§14). A live crash never promotes an unapproved staged change: the standby always runs the
live release.

## 3. Releases and snapshots

### 3.1 What a release contains

```json
{"id":"…","digest":"sha256:…","kind":["lineup","schedule"],
 "sched_gen": 13, "base_release_id":"…",
 "streaming": {"version":"0.42.1","sha256":"…"},
 "settings": {"tv":{…}, "tv_output":{…}},
 "lineup_digest":"sha256:…"}
```

and one `tv_release_channels` row per channel of the lineup:
`def` = the full `channels` row the release wants (id, number, name, slug, mode, mode_config incl.
look and filler lists, source_filter, transition, enabled, sort_order) plus `art` (logo, watermark,
loading picture as content digests; files are content-addressed so old releases keep their art)
and `sched_changed` (whether this channel's schedule differs from the base release after its
divergence boundary).

Filler pools (`filler_pools`/`filler_items`) are referenced by id. A release never deletes pools or
items: removals are deferred to GC after the base release is past retention (§4.7), because
generation N still references them.

### 3.2 Building

`tvrelease.build(base, edits)` runs in the pipeline (it already owns `write_channels`' logic):
start from the base release's snapshot (or from `channels` + `app_settings` when there is none:
release 1 is "what is live now", created by migration-time bootstrap), apply the edits (a
`write_channels` change set, editor saves, a settings diff, a binary version), compute the digest.
Equal digest → reuse the release. Then allocate `sched_gen = nextval('tv_sched_gen_seq')` and
build the generation (§4.3).

### 3.3 Binaries

Release binaries live in `bin/releases/<version>/` next to the current install (Epic 28.3 downloads
there instead of replacing in place — story 33.14). The supervisor refuses to start a slot whose
binary's SHA-256 differs from `tv_releases.streaming.sha256`. Kept: the live and previous versions
and the last `keep_versions` (3).

## 4. Schedule generations

### 4.1 Schema (slot 0124)

- `channel_programs.sched_gen bigint not null default 0` — every existing row is generation 0.
- Unique `(channel_id, sched_gen, seq)`; time index `(channel_id, sched_gen, start_at, end_at)`;
  both built `CONCURRENTLY` (the 0082 pattern), the old indexes dropped afterwards.
- `channel_schedule_state` PK `(channel_id, sched_gen)` — each generation has its own cursor,
  horizon, stale flag.
- `tv_live_gen()` — `STABLE` SQL function returning `tv_routing.live_gen` for scope `cluster`
  (0 when the row is absent, i.e. staging never used).
- `channel_programs_live` view = `WHERE sched_gen = tv_live_gen()`.

### 4.2 Readers

| Reader | Today | After |
|---|---|---|
| Slot engine (live join, `repo_pg.go`) | `channel_programs` | `sched_gen = release.sched_gen` (§2.3) |
| Front door XMLTV/M3U (`hdhr/guide.go`, `hdhr/repo_pg.go`) | `channel_programs` | `channel_programs_live`; staging preview uses the staged generation |
| API guide / channel schedule (`api/internal/handlers/guide/*`, `channels/schedule.go`, `builder.go`, `repo.go`) | `channel_programs` | `channel_programs_live`; `?gen=staging` for admins (Changes page) |
| Scheduler pass (`channels/repo.py`, `base.py`) | `channel_programs` | explicit `sched_gen` parameter on every query |
| `queues/history.go`, `tools/queue_transcription.py` | counts | live view |

### 4.3 Creating generation N+1

For each channel of the release, with `T` = the planned swap time (`swap_at`, or now + expected
verification time when unscheduled):

```
B_c := end of the block of generation N on air at T            -- divergence boundary
copy N's blocks with start_at < B_c into N+1 (same seq, start, end, video, offsets)
if sched_changed(c):  run_schedule(repo(gen=N+1), c, anchor = B_c)    -- new tail from B_c
else:                 copy N's blocks with start_at ≥ B_c too
channel_schedule_state(c, N+1) := cursor after the copy / regeneration
```

`run_schedule` already anchors after the block on air ("a regeneration starts after the block on
air: it is kept (D3)"); it gains `gen` and an explicit `anchor` argument. New channels (lifecycle
`staged`) have no generation N; their N+1 starts at "now" (they are not on air before the swap).

When `swap_at` moves, the generation is **re-anchored**: blocks of N+1 between the old and the new
`B_c` are replaced by N's, the tail is regenerated after the new `B_c`. If T passes before the swap
(e.g. approval is late), the swap planner re-anchors before switching (§8.2), so a channel always
switches on a boundary where N and N+1 agree on everything before it.

Because N+1 ≡ N before `B_c`, **both slots play the same programme at the same position until
`B_c`**: a switch at any instant before `B_c` changes the encoder, not the content.

### 4.4 Top-up of open generations

`topup_once` iterates `(channel, gen)` over the open generations — live, staging, previous
(`tv_routing.live_gen`, the in-flight change's release, `previous_gen` while inside
`previous_until`) — each with its own advisory lock key `(TOPUP_KEY, hash(channel, gen))`. The
7-day horizon (tv-core) makes this rarely needed during a swap window, but a change can sit in
`ready` for days waiting for approval.

### 4.5 Lineup lifecycle

`channels` stays the live lineup that every non-slot reader uses (HDHR `lineup.json`, the API).
Staged additions need a `channels` row for the FK of `channel_programs`, so:

- `channels.lifecycle`: `staged` (exists only for a staged release), `live`, `retired`.
- Number and slug unique indexes become partial `WHERE lifecycle = 'live'`; a staged channel's
  number/slug in `channels` are placeholders — the real values are in `tv_release_channels.def`.
- Every live reader filters `lifecycle = 'live'` (HDHR lineup, guide, API lists, `write_channels`
  `_actual`).

### 4.6 Apply at the swap

In the swap transaction (§8.3):

```
UPDATE channels SET (number, name, slug, mode, mode_config, …) = def, lifecycle='live'
       FROM tv_release_channels WHERE release_id = R_new        -- adds, renumbers, edits
UPDATE channels SET lifecycle='retired', retired_at=now()
       WHERE lifecycle='live' AND id NOT IN (release channels)   -- removals
UPDATE app_settings rules.tv / rules.tv_output := release.settings (when kind ⊇ settings)
UPDATE tv_routing SET live_release_id=R_new, live_gen=N+1, previous_*=old, epoch=epoch+1, …
```

Renumbering inside the partial unique index is done in two steps inside the transaction (numbers
moved to negative placeholders first). A retired channel keeps its row (and generation N's blocks)
until retention ends, so a rollback can bring it back.

### 4.7 Garbage collection

A daily sweep deletes: blocks of generations that are neither live, staging nor within
`rollback_retention_hours` of being superseded; `retired` channels past retention (cascade deletes
their programmes); `staged` channels of cancelled/failed changes; filler pools/items whose removal
was deferred and no retained generation references; release binaries beyond `keep_versions`.

## 5. Splice-ready output

Today each station's PTS starts from the station's own epoch (`in.TSOffsetSec = start − s.epoch`)
and keyframes are forced every `keyintSeconds` from the start of each block
(`expr:gte(t,n_forced*2)`), so two stations of the same channel started at different times have
unrelated timestamps and keyframe instants.

Story 33.4 changes three things, in the encoder arguments only:

1. **Channel timeline epoch** = a fixed instant (Unix epoch): `TSOffsetSec = start.Unix()` (mod
   2³³/90 kHz ≈ 26.5 h, wrap handled as players already do). The same wall-clock instant has the
   same PTS in every slot and host.
2. **IDR grid**: `-force_key_frames expr:gte(t+<offset>, n_forced*<keyint>)` with the block's
   wall-clock offset, so IDRs fall on wall-clock multiples of `keyint_seconds` (default 2), and
   GOPs are closed (`-flags +cgop`, `-bf 0` already in tv-core's args).
3. **Fixed PIDs and stream ids** (`-mpegts_pmt_start_pid`, `-mpegts_start_pid`, service id) set
   explicitly so a new binary cannot change them silently.

HLS segment boundaries then fall on grid IDRs in both slots (the segmenter cuts at the first IDR
after `hls_time`), and `PROGRAM-DATE-TIME` agrees across slots to the frame.

When alignment does not hold (a release changes `keyint_seconds` or fps, or a live block whose
yt-dlp source cannot be cut on the grid), splicers fall back to re-timestamping (§8.5).

## 6. Warm-up and verification

### 6.1 Warm set

The front door computes the warm set every 5 s while a change is `verifying` or `ready`:

```
demand  := channels with a front-door client attached, an HLS touch within the warm grace,
           or a running push                                       (Epic 32 §6.1 demand)
changed := channels with sched_changed, def changed, or all channels for settings/binary kinds
probe   := tv_staging.probe_channel or the lowest-numbered channel
warm    := demand ∪ {probe} ∪ (rolling window over changed \ demand, verify_probe_seconds each)
budget  := staging_max_extra_encodes or the sessions left after live and the standby's warm set (§14)
```

Demand channels have priority; if demand alone exceeds the budget, the change is still verifiable
(the excess is probed in turn) and the swap becomes a **rolling swap** (§8.6). For each warm
channel the front door writes a staging placement (`track = staging`) — by default on the host of
the live placement (no host change at the swap), else wherever the balancer finds room — and calls
`warm` on that slot.

### 6.2 Checks

The front door runs probes against the staging upstream (never through client paths) and the API
aggregates them into `tv_changes.verify_report`:

| Check | Pass rule | Fail / warn |
|---|---|---|
| `slot.health` | `/readyz` 200, version = release, release id = change's | fail |
| `channel.first_segment` | playlist lists ≥ 1 segment within the ready timeout (tv-core) | fail |
| `channel.cadence` | segment durations within `hls_time` ± 1 GOP over the soak | warn, fail if a gap > 2 × `hls_time` |
| `channel.wallclock` | `|PDT(last seg) + dur − now| < hls_time + 1 s`; position vs schedule < 1 s | fail |
| `channel.block` | the slot's `status` reports the block of N+1 at now; for unchanged channels the same block id **and** PTS within 1 frame of live's | fail |
| `ts.conformity` | 10 s of `live.ts`: PAT/PMT every ≤ 0.5 s, PIDs as configured, H.264 profile/level, resolution/fps = `tv_output`, IDR every `keyint` on grid, closed GOP, no B-frames, CC errors 0, PCR monotonic | fail |
| `hls.conformity` | `EXT-X-PROGRAM-DATE-TIME`, `INDEPENDENT-SEGMENTS`, target duration, segment starts with IDR | fail |
| `splice.compat` | stream parameters (codec, profile, resolution, fps, audio codec/rate/channels, PIDs) equal live's → `seamless`; otherwise `param_change` | info / warn |
| `cloudflare.ingest` | H.264; AAC (or AC-3 with relay audio transcode); closed GOP 2–10 s; combined bitrate 2–14 Mb/s; no B-frames if `tv_remote.low_latency` | fail when remote is enabled; warn otherwise |
| `push.continuity` | for each channel with a running push: `seamless` splice, or `param_change` with `param_change_push` resolved (ack or defer) | fail until acknowledged |
| `guide.coverage` | N+1 covers each channel to the guide horizon (7 days), no gaps/overlaps | fail |
| `guide.vs_stream` | for warmed channels, the XMLTV programme at now (rendered from N+1) = the slot's block | fail |
| `lineup.sanity` | numbers and slugs unique, each channel has a logo, ≤ `max_channels` (channel lineup rules), no channel without programmes | fail / warn |
| `encoders` | no NVENC→software fallback, no muxer restart during the soak | warn |
| `soak` | all of the above hold for `verify_soak_seconds` | — |

Verification passes when no check fails. Warnings are shown to the approver; if an auto-promote
rule has been switched on (all are `approval` by default), warnings block it.

### 6.3 Staging never pushes

The push relay (§9) subscribes only to routed (live) upstreams. Staging slots have no pusher, no
call path to `tv_push_credentials` (it answers only the front door's role), and their placements
are `track = staging`, which the remote controller ignores.

## 7. Preview

- `POST /api/tv/changes/{id}/preview` returns signed URLs valid for 1 h:
  `/stream/staging/channel/{id}/index.m3u8?pt=…`, `/staging/lineup.m3u?pt=…`,
  `/staging/xmltv.xml?pt=…`. On the web port the `/stream/staging/` path additionally requires an
  admin session (the stream gate checks the role).
- The front door serves preview from the staging placement (warming the channel if needed, within
  the budget; a preview viewer counts as demand for the staging track only).
- No staging HDHomeRun device (decision 10: browser preview only; VLC can use the staging M3U).

## 8. The swap

### 8.1 Routing state

```sql
tv_routing(scope text primary key,             -- 'cluster' | 'host:<id>'
           live_release_id uuid, live_gen bigint,
           previous_release_id uuid, previous_gen bigint,
           epoch bigint not null, switched_at timestamptz, previous_until timestamptz)
tv_channel_switch(change_id uuid, channel_id uuid, switch_at timestamptz,
                  state text check (state in ('planned','switching','switched','skipped')),
                  switched_at timestamptz, primary key (change_id, channel_id))
```

The front door caches routing and LISTENs on `tv_routing`; every routing decision carries the epoch
it was made under.

### 8.2 Planning

At `promote` (an admin's approval with `swap_at` reached, or "Swap now"; an auto rule only if one
was switched on) the controller asks the front door
for a plan:

```
for each channel c of the union of both lineups:
   if c has no demand:                    switch_at := T_swap        (instant, nothing to splice)
   elif swap_wait == immediate:           switch_at := T_swap
   else:                                  switch_at := min(B_c(T_swap), T_swap + max_wait)
   re-anchor N+1 for c if B_c moved since staging (§4.3)
```

Removed channels get `switch_at = B_c` too (their viewers finish the programme on the old slot,
then the front door ends TS connections and answers HLS 404/`ENDLIST`). Added channels switch at
`T_swap` (they have no live viewers).

### 8.3 Commit

At `T_swap` the controller runs **one transaction** under `pg_advisory_xact_lock('tv_swap')`:
§4.6 apply, `tv_routing` (new live release/gen, previous = old, `epoch + 1`, `previous_until = now +
keep_warm`), `tv_slots.role` (staging → live, old live → previous, old standby → draining), placements
(`track staging → live`, old live → previous, old standby released), `tv_channel_switch` rows, change `swapping`. NOTIFY `tv_routing`. Then it enqueues
`plex_livetv` (`reason: tv_swap`, force: channel map + guide reload).

The guide and lineup change at commit; for channels that switch at `B_c` later, guide and stream
agree because N+1 ≡ N before `B_c`.

The front door then performs each channel's switch at its `switch_at` (§8.4–8.5) and marks it
`switched`; when all are switched the change becomes `live`. A channel's switch is idempotent and
fenced by `epoch`.

### 8.4 HLS stitching

The front door serves `/stream/channel/{id}/index.m3u8` itself (no longer passes the slot's
playlist through verbatim):

```
state per channel: out_seq (front door media sequence), disc_seq, list of (slot, upstream seg name,
                   duration, PDT, discontinuity_before)
on every upstream playlist poll (≤ 1 s, or on request):
   append new upstream segments of the current source with out_seq++
on switch at instant t:
   new source := staging; first := first staging segment with PDT ≥ end(last listed live seg)
             (with aligned output this is the segment starting exactly there)
   append first with discontinuity_before = true
playlist := EXT-X-MEDIA-SEQUENCE = out_seq of the first listed entry,
            EXT-X-DISCONTINUITY-SEQUENCE = disc_seq (+1 each time an entry with
            discontinuity_before slides out), entries with URIs /stream/channel/{id}/s/{slot}/seg-N.ts
```

Segment requests `…/s/{slot}/seg-N.ts` are proxied to that slot (so a player that fetched an old
playlist still gets old segments while the old slot is in the `previous` role). The upstream epoch-based segment
numbers are not exposed. A player sees one `EXT-X-DISCONTINUITY` (players handle it as they do at a
muxer restart today), no media-sequence reset, and the wall-clock position continues.

### 8.5 TS splicing

The splicing copier (Epic 32 §4.2, extended):

```
current upstream U (live), client connection W
on switch at instant t:
   open S (staging) TS; read until S's first IDR with PTS ≥ PTS_t(U) — buffered, not sent
   keep copying U until U's next grid IDR (the packet with random_access_indicator, PTS = P)
   if S has an IDR at exactly P (aligned):   offset := 0
   else:                                      offset := last U PTS + frame − first S IDR PTS
   write PAT/PMT (same version if identical, else version+1 and discontinuity_indicator)
   from S's IDR at P: rewrite CC per PID to continue U's, apply offset to PTS/DTS/PCR (0 normally)
   close U
```

At most one GOP (2 s) is buffered. Audio packets of S before the video IDR are dropped; the first
audio frame at or after P follows. Tuner leases are held by the front door connection, so Plex
sees nothing but the bitstream continuing. The same copier serves failover (32.4), where U simply
ends.

### 8.6 Rolling swap

When the warm budget cannot hold every watched channel twice, channels are switched in order of
`switch_at`: for channel c, staging warms c at `switch_at − warm_lead` (first segment ready time +
margin, default 20 s), the switch happens, and the old slot's station for c is released at once
(not kept for the previous slot; rollback for it is cold for its first minutes). The planner orders and
spreads `switch_at` (only in `immediate` mode; boundary mode already spreads them) so that extra
encodes stay ≤ budget.

### 8.7 Multi-host

- Releases with lineup/schedule/settings kinds are **cluster-wide**: one `tv_routing` row, one
  commit; each channel's staging placement may be on any host.
- A binary-only release can be **per host** (`scope = host:<id>`): only that host's slots swap; the
  cluster routing row is untouched.
- When the staging placement of c is on another host than its live placement, the switch is the
  same operation with a different upstream URL (signed node request). The live placement is
  released after the previous slot period; Epic 32's "a placed channel is sticky" holds for the new
  placement.

### 8.8 Sequence (one watched channel, boundary mode)

```
admin approve ─► controller: plan (B_101 = 20:30) ─► commit at T (routing epoch 8→9, guide = N+1)
                                                     plex_livetv: channel map + reloadGuide
20:29:40  front door: staging station of 101 warm (already, verified), aligned upstream open
20:30:00  block boundary: HLS → discontinuity + staging segments; TS → splice at IDR 20:30:00.000;
          relay → source switch at the same IDR; tv_channel_switch(101) = switched
20:45:00  previous_until: old slot drains its stations, stops; change stays live
```

## 9. Push relay — remote and simulcast continuity

### 9.1 Ownership

Epic 32 §7.4 puts the pusher on the encoding host as a Hub subscriber. With slots, that would tie
the RTMPS session to the slot process, and a swap (or a slot crash) would end the session —
Cloudflare would start a new video after `timeoutSeconds`, remote tokens whose `sub` is the video id
would die, and every simulcast output (Epic 32 story 32.13: YouTube, Facebook, custom RTMP attached
to the live input) would disconnect from its platform.

So the push moves into the **front door's push relay** on the home:

```
routed upstream TS (live slot, any host) ─► splicer/retimer (Go, §8.5) ─► stdin of one long-lived
ffmpeg -f mpegts -i pipe:0 -c:v copy -c:a copy|aac … -f flv rtmps://…/<key>   (or SRT)
                                                         └──► Cloudflare live input ──► HLS viewers
                                                                                  └─► simulcast outputs
```

The relay is a client of the routing layer like a TS viewer: at a swap, rollback or failover it
switches its source with the same splice (§8.5), so ffmpeg's input is one continuous MPEG-TS with
monotonic timestamps, and the RTMPS/SRT session, the broadcast, the video id, the tokens, the
recording and every simulcast output continue. **The simulcast outputs are protected by exactly
this:** they hang off the live input, and the live input never sees its publisher disconnect.

The relay holds the push's demand (`reg.attach` equivalent through a front-door hold) on whichever
slot is live, and pulls node-encoded channels over the signed LAN path (one more LAN hop, README decision 5).

### 9.2 Parameter changes

| Change in the release for a pushed channel | Relay action | Cloudflare / outputs |
|---|---|---|
| none / bitrate / art / schedule (`splice.compat = seamless`) | in-session source switch | unbroken |
| resolution, fps, profile, audio layout (`param_change`) | in-session switch if the 33.8 spike shows Cloudflare accepts a mid-stream sequence-header change; otherwise stop + reconnect immediately (< 2 s) | same video id (inside `timeoutSeconds` 30); simulcast outputs may reconnect → requires explicit ack (`param_change_push = approve`) or `defer` (that channel switches when its push ends) |
| codec not accepted by Cloudflare | verification fails (`cloudflare.ingest`) | never reaches the air |

AC-3 output: the relay's ffmpeg transcodes audio to AAC continuously; its input timestamps are
continuous, so the AAC stream is continuous too.

### 9.3 Relay robustness

- Relay ffmpeg exits → restarted at once on the same input with the same key; the gap is under 2 s
  ≪ 30 s, so the same video continues (Epic 32 §7.3 already relies on the reconnect window).
- Front door restart (its own upgrade, a crash) → every relay reconnects within the restart time;
  the supervisor restarts the front door first and fast (no encoders in it), well inside 30 s.
- Push credentials (`tv_push_credentials`) are requested by the front door's role only.

## 10. Rollback

| Kind | When | What happens |
|---|---|---|
| **Warm** | `now < previous_until` and the previous slot is healthy | A swap in reverse: commit restores the previous lineup snapshot (`tv_release_channels` of the previous release) into `channels` (un-retiring removed channels, `staged`-izing added ones), `live_gen := previous_gen`, `epoch + 1`, roles swap; switch per channel **immediately** (boundary waiting is skipped: the old content is the content of N up to now, which equals N+1 before `B_c`; for channels already past `B_c` it is a mid-programme splice back to N's programme at the same wall-clock position); `plex_livetv` again. Channels the previous slot no longer holds warm are warmed first (≤ 10 s) or switched cold. |
| **Cold** | after the grace, within retention | A new TV change `rollback` with `release = previous`, generation re-anchored (a *new* `sched_gen` copied from the retained N, so the timeline continues from now); normal verify → swap; the admin's rollback request is its approval (`require_second_admin` still applies). |
| **Automatic** | in the grace: live slot crash (watchdog), crash loop (2 in 10 min), or post-swap health failure (any `channel.*`/`ts.*` check failing on the new live) | Warm rollback, `end_reason = auto:<cause>`, notification. Outside the grace: no automatic rollback (the watchdog restarts the slot). |

Rollback keeps R7: the relay switches source back with the same splice, so remote viewers and
simulcast outputs see nothing.

## 11. TV changes

### 11.1 Store

`tv_changes` (slot 0123) holds the object; `tv_change_events` every transition; `audit_log` every
admin action (category `tv_change`, `target_kind = tv_change`).

### 11.2 Transitions (controller, `api/internal/tvchange`)

| From → to | Trigger | Guard |
|---|---|---|
| — → `draft` | producer writes (§11.4), admin "new" | at most one open draft per author source; editor saves append to the open draft |
| `draft → staged` | Stage (admin) or auto (producer in `auto` mode) | no other change in `staged…swapping`; release builds; generation built |
| `staged → verifying` | slot started on the release and healthy | a slot instance that is neither live, standby nor previous is free |
| `verifying → ready` | report has no fail | — |
| `verifying → failed` | a fail | slot stopped, staging placements released, staged channels deleted |
| `ready → approved` | Approve | admin; second admin when required; param-change ack when needed |
| `ready → approved` is required by default for every kind | | |
| `approved → swapping` | Swap now, or `swap_at` reached | an admin approved (all rules are `approval` by default) |
| `ready → swapping` | an auto rule satisfied | only if the admin switched a rule on: rule allows all kinds, no warnings, in the quiet window |
| `swapping → live` | all channel switches done | — |
| `live → superseded` | the next change goes live | — |
| `swapping/live → rolled_back` | rollback (manual/auto) | live only while it is the current live change |
| `* → cancelled` | Cancel | not in `swapping`/`live` |

`ready` re-checks continuously (a cheaper subset every 60 s: health, wallclock, block); a check that
turns fail moves the change back to `verifying`. A `ready` change older than its `swap_at` + 1 h with
no approval stays `ready` and notifies daily.

### 11.3 Auto-promote

**Default (owner decision): every kind is `approval`** — nothing reaches the air without an admin.
The rules remain settings: the strictest `tv_staging.auto_promote` rule among the change's kinds:
`approval` → wait for an admin;
`quiet_window` → promote at the first moment inside the window with ≤ `quiet_max_viewers` viewers
and no warnings; `immediate` → promote when ready and no warnings. An explicit `swap_at` overrides
the window (still requires the rule's approval if `approval`).

### 11.4 Producers

`apply_or_review(ctx, changes, apply)` stays the seam. With `tv_staging.enabled`:

- `write_channels.run` passes an `apply` that **stages** instead of writing: it builds a TV change
  (`source = {job_id, stage, plan_id, digest}`, `change_set = changes.to_json()`) and a release
  from the base release + the change set. `write_mode = auto` → the change goes straight to
  `staged`, is verified, and waits in `ready` for an admin (default rules). `write_mode = review` → today's path
  parks the digest; the TV change is created as `draft` with `digest` so the Changes page can show
  the diff; **Approve on the Changes page posts `{apply:true, plan_digests:[digest]}` to the job
  (the queue's Apply)**, which re-runs the job, whose `apply` now stages; the controller links the
  staged change to the approval (no second approval needed for that change).
- `_verify` (read-back) checks the release snapshot instead of `channels`; the read-back of
  `channels` happens after the swap commit (controller).
- The channel editor (`api/internal/handlers/channels/*`), filler pools and art overrides write into
  the open draft's pending snapshot (`tv_changes.diff.pending`), not `channels`; the editor shows the
  draft values. `stale` stops rewriting the live generation: it marks the draft channel
  `sched_changed`.
- Settings saves of `rules.tv` and `rules.tv_output` (Settings page) create/append a `settings`
  draft when staging is on; other rules sections are unaffected.
- `plan_channels` is unchanged (its output, `channel_plans`, is not on air).
- **What is not a change.** The scheduler's routine top-up — extending the existing grid with the same
  lineup, rules and content selection — writes every open generation (§4.4) and never creates a
  change. A *schedule change* is a change to a channel's grid, `rules.tv`, mode/slots or content
  selection (sources, collections, filler lists), or a forced regeneration (`stale`); those go through
  a change.
- `dizquetv_*` extension stages are out of scope (they write to dizqueTV, not to Maktaba's air).

### 11.5 Queueing

One change in `staged…swapping`. A new draft created meanwhile records `base_release_id` = the
in-flight release when it is staged later; if the in-flight change fails or is cancelled, the draft
is re-diffed against the live release. The Changes page shows the queue.

## 12. Watchdog interplay and failure modes

The supervisor (process watchdog) restarts any child with backoff and probes `/healthz`. Epic 33
adds rules on top:

| Failure | Effect | Handling |
|---|---|---|
| Staging slot crash / hang | none for viewers (no client reads it; the relay does not use it) | watchdog restarts it; the change returns to `verifying`; 3 crashes during verification → `failed (slot_crash_loop)` |
| Live slot crash, outside grace | the front door's upstreams end | **hot standby (default on the home)**: the front door switches every channel to the standby at once (§2.4) — HLS stitch, TS splice, relay source switch; the crashed instance restarts as the new standby. Without a standby (nodes, or `hot_standby = false`): the watchdog restarts the slot (~1–3 s) and the front door re-resolves; viewers stall one ready timeout; TS connections are kept (up to 3 splices); pushes keep their session if the gap < 30 s |
| Standby slot crash | none for viewers | watchdog restarts it; it re-warms; failover is unavailable meanwhile (event, slot bar shows it) |
| Live slot crash, inside grace | — | automatic warm rollback to the previous slot (§10) |
| Live slot crash while a change is staged | — | failover to the hot standby (live release); the staged change is never promoted by a crash, approved or not (decision 11); its verification continues |
| Front door crash | TV clients disconnect; slots keep encoding | watchdog restarts it first; Plex retries `/auto`; HLS players retry; relays reconnect within 30 s (same video id) |
| Both slots on one host down | channels of that host fail over to other hosts (Epic 32 §6) | — |
| Swap commit fails (DB error) | nothing switched (single transaction) | change returns to `ready`; event `tv_swap_failed` |
| A channel's staging upstream fails during its switch | that channel stays on live | retry once after 5 s; then `skipped`; the change completes as `live` with `skipped` channels switching cold at their next viewer-less moment, or rollback if > 25 % skipped |
| Database unreachable | slots keep running stations from in-memory schedules (today's behaviour) | swaps are not possible; no routing changes |
| Clock jump on the host | PDT/PTS alignment is from wall clock | verification `channel.wallclock` fails; swap waits |
| Disk full in a slot's dir | that slot's HLS fails | watchdog/health → unhealthy; staging: change fails; live: alert |

## 13. Upgrades

Epic 28.3's update path changes from "replace this executable, re-exec" to:

1. download the release to `bin/releases/<version>/`, verify checksums;
2. create a `binary` TV change with `streaming = <version>` and the rest of the live release;
   stage → verify (the new binary runs as the staging slot) → wait for an admin's approval (owner
   decision; the `binary` auto rule is `approval` by default) → swap;
3. after the TV swap: restart `api`, `pipeline`, `web` on the new version (supervisor rolling
   restart; their clients retry);
4. the front door restarts only if `edge_protocol` (a constant in the binary) changed, in the quiet
   window, after the TV swap;
5. the supervisor itself (`maktaba-server`) re-execs last (Epic 28.3), keeping children running
   (they are re-adopted by pid file) — or restarts them in order if re-adoption is not possible on
   the OS (Windows: children are restarted, slots first via a swap is not needed since the slot
   binary is unchanged).

Failed verification → the TV part aborts; the other roles are not restarted; the admin sees the
report.

## 14. Resources

- Encoder budget per host: `capacity` (tv-core `EffectiveMaxActive`, NVENC session cap / 2) shared by
  all slots, allocated in order: **live** → **standby warm set** → **staging** (extra encodes ≤
  `staging_max_extra_encodes`, auto = what is left). Live never loses a session to the others.
- Load per phase (W = live warm channels): steady 2 × W (live + standby); change staged 2 × W +
  staging set; grace 2 × W (new live + previous; the old standby drains at the switch); after the
  grace 2 × W again (the new standby starts). Never three full copies.
- The planned **22 always-on channels pushed to Cloudflare** make W = 22: 44 encodes with a full
  standby. tv-core's VideoToolbox cap is 16 per host, so: live channels beyond the home's cap spill to
  nodes (Epic 32); the standby warms `viewers` first; channels not on the standby fail over by restart;
  staging uses rolling probes and a rolling swap (§8.6). How many real-time encodes the M3 Ultra's
  VideoToolbox sustains is an open measurement (README question 12); **Intel QSV** (`h264_qsv`) on an
  additional streaming node is the future capacity path.
- NVENC (consumer cards, session cap): rolling swap; the standby is off by default on nodes.
- Memory/disk: each slot's HLS window is small (`hls_list_size` × segments); standby keeps only
  watched channels.
- CPU of the front door: stitching and splicing are packet-level copies; the relay is stream copy
  (plus AAC when needed).

## 15. Observability

Metrics (Prometheus names, `maktaba_tv_`): `slots{host,slot,role,state}`, `slot_release{host,slot}`,
`changes{state}`, `verify_checks_total{check,result}`, `swap_duration_seconds`,
`channel_switch_latency_seconds` (switch_at → switched), `ts_splices_total{reason}`,
`hls_stitches_total{reason}`, `relay_source_switches_total{mode=seamless|retimed|reconnect}`,
`relay_reconnects_total`, `rollbacks_total{reason}`, `staging_extra_encodes{host}`.
Events: `tv_change_<state>`, `tv_slot_started|crashed|stopped`, `tv_swap_planned|committed|channel_switched|failed`,
`tv_rollback_*`, `tv_push_source_switch`, `tv_push_reconnect`. The control centre's health samples
gain the slot bar.

## 16. Testing approach

- **Unit (Go)**: playlist stitcher (sequence, discontinuity sequence, PDT alignment, slide-out),
  TS splicer (aligned and retimed, CC continuity, PAT/PMT versioning, audio alignment), routing
  epochs/fencing, warm-set computation, rolling plan.
- **Unit (Python)**: generation copy-to-boundary, re-anchoring, top-up of open generations, release
  digest, write_channels staging apply, GC.
- **Integration**: two fake-ffmpeg slots (the existing `fakeffmpeg_test.go` harness, extended to emit
  aligned timestamps), a front door, a Postgres; swap with an HLS client and a TS client.
- **E2E push**: a fake RTMP/SRT ingest (a small Go RTMP server in tests, or `ffmpeg -listen 1`
  writing FLV) checks **one continuous session**, **monotonic timestamps** and no gap > 1 GOP across a
  swap and a rollback; a fake simulcast output fed from the fake ingest (re-published to a second
  fake server) checks **no disconnect**.
- **Chaos**: kill staging during verification; kill live in and outside the grace; kill the front
  door; DB failure at commit.
