# Epic 31 — Multi-instance Maktaba (one home, many workers on the LAN)

**Goal.** Several Maktaba installs on one local network — macOS, Linux, Windows — find
each other, share one library on the shared storage (the NAS), and split the work
through the queue: each machine runs the stages it is good at (the Mac's GPU
transcribes, the machine next to the NAS hashes and makes thumbnails, any machine
tags), with nothing to configure by hand beyond approving a machine once.

**Decisions (owner, 2026-10-18).**

| Question | Decision |
|---|---|
| Topology | **One home, others join.** The home instance holds the database and the queue; the others discover it and join as workers on the same database. No replication, no second catalog. |
| Joining | **Approved with a pairing code.** A machine that asks to join shows a code; an admin enters it on the home to approve. Nothing joins silently. |
| Shared storage | **Shared catalog + generated files.** Every instance sees the same library; generated files (subtitles beside the video) are written once to the share; each machine maps the share's path for its OS automatically. Media files are never moved. |
| Work split | **By capability, with overrides.** A machine offers the stages it can run; an admin can switch stages on or off per machine. |

**Anchors.** Builds on the queue engine (plan 06-11: claims, pools, worker registry,
path maps), plan 03-10 (transcription checkpoints, worker slots), Epic 15 (service type
`_maktaba._tcp`, pairing codes, server keys). Supersedes the coordinator layout of
plan 19-04 (the engine already has claims, pools and a registry).

**Documents.** Phase 1 (joining, built): [design.md](design.md) (architecture, protocol,
schema, security, placement, cross-platform) · [plan.md](plan.md) (tracks, tasks, tests).
Phase 2 (the cluster channel: live metrics, logs and jobs, files on the share, LLMs, the
hub): [design-agent.md](design-agent.md) (requirements trace, protocol, files, hub, LLMs) ·
[plan-agent.md](plan-agent.md) (tracks T4–T6, H1, P1, end-to-end checklist). The service door
(machines call Plex through the home, which adds the token): [story](story-31-relay-service-door.md)
· [plan](plan-31-relay-service-door.md). User
documentation: [docs/cluster](../../../docs/cluster/README.md).

---

## Requirements

### R1 — Discovery
- R1.1 The home advertises itself on the LAN by mDNS/DNS-SD (`_maktaba._tcp`), with its
  role, version, schema revision and whether it accepts joins; never a library name.
- R1.2 A machine running `maktaba-pipeline join` lists the homes it finds within a few
  seconds; with one it is chosen, with several the user picks; `--home URL` skips
  discovery (networks that block multicast). A subnet probe is the fallback.
- R1.3 Works on macOS, Linux and Windows (Python, no OS service required to browse).

### R2 — Joining (pairing)
- R2.1 Joins are off until an admin turns on "accept new machines" on the home (off by
  default, as every cross-instance feature in Epic 15).
- R2.2 A join request carries the machine's identity (stable node id), name, OS,
  architecture, versions, capabilities and the storage it sees; it appears on the home
  as *pending*.
- R2.3 The machine shows a 6-digit pairing code derived from both sides' keys; the admin
  approves by entering that code on the home. A wrong code is refused; five wrong codes
  reject the request. A request expires after 15 minutes.
- R2.4 On approval the machine receives, encrypted to its own key, the credentials to
  reach the home's database (its own database role) and its starting configuration; it
  stores them in its config directory and starts working.
- R2.5 An admin can revoke a machine at any time: its database role stops working at
  once, its claimed jobs return to the queue, its row stays (revoked) for the record.
- R2.6 A machine can be renamed, drained (finish what it has, claim nothing new) and
  removed.

### R3 — Shared storage
- R3.1 There is one catalog: every machine reads and writes the home's database.
- R3.2 Each machine works out where the share is mounted locally (macOS `/Volumes/…`,
  Linux `/mnt/…`, `/media/…`, Windows `Y:\` or `\\nas\share`) by finding known library
  files, and proposes the path map; the admin can edit it.
- R3.3 A machine checks its storage at start and every few minutes: readable (and,
  for stages that write next to the media, writable). A machine whose storage is not
  reachable stops claiming storage stages and says why.
- R3.4 Generated files that belong with the media (subtitles) are written to the share
  by whichever machine makes them; per-machine caches (thumbnails, review pages, the
  vector index) stay on the home.
- R3.5 Media files are never moved or renamed by this feature.

### R4 — Splitting the work
- R4.1 Each machine reports what it can do: OS, CPU, memory, GPU / Apple Silicon,
  installed extras (transcription, search), ffmpeg, storage access.
- R4.2 The stages a machine claims = the stages it can run ∩ the stages allowed on it
  (home-only stages: the vector index, thumbnails, review pages) ∩ the admin's
  per-machine switches. Changes apply within a minute without a restart.
- R4.3 Global limits (NAS readers, network services) hold across all machines;
  per-machine limits (CPU, GPU batch) are each machine's own.
- R4.4 Work is never lost when a machine leaves: claims are reaped, transcriptions resume
  from their checkpoints on another machine.
- R4.5 A machine running older code never claims a stage whose version moved on, and
  never rewrites the stage registry.

### R5 — Visibility
- R5.1 An admin page lists this home and every machine: status (pending, online,
  offline, draining, revoked), name, OS, capabilities, storage check, path map, the
  stages it serves and why others are off.
- R5.2 The Processing page names the machine serving each queue.

### R6 — Security
- R6.1 No credential crosses the network in the clear: the database credentials are
  sealed to the machine's key; the pairing code defends against a machine in the middle.
- R6.2 Each machine has its own database role (data access, no schema changes);
  revocation disables it and ends its sessions.
- R6.3 The home's join endpoints are rate-limited and answer only while joins are on.

## Out of scope (follow-ups)
Database TLS (sslmode=require with the home's certificate); a Windows service installer
for workers; the desktop app running a worker; moving per-machine caches to the share;
federation between independent instances (Epic 15.3/15.7).

## Acceptance
- A Linux (or Windows) machine runs `maktaba-pipeline join`, finds the Mac home, shows a
  code; the admin enters it; the machine starts claiming the stages it can run, with
  its path map worked out, and appears online on the Instances page.
- Revoking it stops its database access within seconds; its jobs return to the queue.
- Turning a stage off for it on the page stops it claiming that stage within a minute.
- Home-only stages never run elsewhere; transcription runs only on Apple Silicon.
