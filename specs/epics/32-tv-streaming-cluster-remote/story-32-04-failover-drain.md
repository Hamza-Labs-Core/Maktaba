# Story 32.4 — Failover and drain

## Description

*As a viewer, I want the channel I am watching to keep going when the machine encoding it turns
off; as the owner, I want to take a machine out for maintenance without cutting anyone off
needlessly.*

Because every channel is anchored to the wall clock, a channel can restart anywhere and land at
the right position. This story detects dead hosts, re-places their channels, keeps Plex's tuner
connection open across the move, and adds drain and move.

Design: [design.md](design.md) §6.

## Acceptance criteria

- **AC1** A host is down when its last beat is older than `tv_cluster.dead_after_seconds`
  (default 15) or after three consecutive front-door proxy failures; `event=tv_host_down` is logged
  with the reason.
- **AC2** Within 2 s of a host going down, every placement on it with demand (a front-door client
  attached, an HLS touch within the warm grace, or a remote push) is re-placed with
  `reason = failover` and a new generation; placements without demand are released.
- **AC3** The new station joins at the wall-clock position; its HLS muxer starts with a
  discontinuity; media sequence numbers keep increasing.
- **AC4** An open `/auto/v{n}` or `live.ts` client connection is spliced onto the new host's TS
  without closing it (at most 3 splices per connection); Plex keeps playing without re-tuning.
- **AC5** A host that comes back after being declared down finds its placements superseded and
  stops those stations within 5 s (fencing); it takes new channels again once healthy.
- **AC6** `tv_cluster.failover = false` disables automatic re-placement (requests still re-place
  through `resolve`).
- **AC7** Drain (`PUT /api/tv/cluster/hosts/{id} {drain:true}`): the host takes no new channels;
  its channels move when idle, or at once with "move now"; drain survives restarts.
- **AC8** `POST /api/tv/cluster/channels/{id}/move {host_id?}` moves one channel (to the host, or
  the balancer's choice); `DELETE /api/tv/cluster/channels/{id}/placement` stops it everywhere.
- **AC9** A remote push on the failed host restarts on the new host to the same live input (32.7).

## Test cases

- **TC1** `TestDeadAfterMissedBeats` — fake clock; 16 s without a beat → down.
- **TC2** `TestProxyFailuresMarkDown` — three failures → down without waiting.
- **TC3** `TestFailoverReplacesOnlyDemanded`.
- **TC4** `TestSpliceTSOnFailover` — upstream A closes mid-stream → the client keeps reading from upstream B.
- **TC5** `TestComebackHostStopsSuperseded`.
- **TC6** `TestDrainNoNewPlacements` and `TestMoveNow`.
- **TC7** E2E (two processes, one machine): kill the node → Plex-like client continues within 20 s.

## Edge cases

- **EC1** Network partition (the node still runs, the home cannot reach it): the node is declared
  down and its stations stop at their next placement check (the row moved); viewers are on the
  new host.
- **EC2** Every host down except a full home: failover gets `ErrAtCapacity`; the channels with the
  most viewers are re-placed first; the rest answer 503.
- **EC3** Flapping host: after two down/up cycles in 10 min it stays out of placement for 10 min
  (`unfit: flapping`).

## Dependencies

32.2, 32.3.
