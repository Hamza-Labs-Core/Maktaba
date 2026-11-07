# Story 54.40 — Published events, cross-plugin triggers and run-time lineage

> Epic 54 · Phase 1 · Depends on: 54.04, 54.06, 54.08, 54.39 · Needed by: 54.42

## Why

Plugins that work together need to hand work to each other: one finds silences, another
writes chapters from them. Doing that by naming each other's stages would break with every
plugin release and let a plugin reach into another's internals, so it goes through events the
publisher declares, with a payload it promises, and a grant the admin gives. The static cycle
check of 54.39 sees only the declared graph; a loop through data (a write that reruns a core
stage whose event starts the writer again) slips past it, so every job also carries its
lineage and a loop is stopped where it happens (README §4.12, H26).

## Description

### 1. Emitting

- WIT `maktaba:plugin@1.1.0` adds the interface `events` with
  `emit: func(name: string, subject: subject, payload: string) -> result<_, call-error>`.
  A 1.0 guest keeps running: the host links 1.0 and 1.1.
- The host forwards it as `events/emit`. The API allows it only when `name` is in the
  calling stage's `emits`, the subject's kind is the event's, a video subject is in the
  job's read scope, and the payload is a JSON object of at most 16 KB that the event's
  declared schema accepts (the settings' JSON Schema subset, `pluginsettings`). Refusals:
  `plugin_scope` (not declared, wrong subject), `plugin_invalid` (payload), `plugin_quota`
  (over 1,000 requests and events in one job), `plugin_loop` (§3).
- What the API allowed comes back in the host's `result` as `events`, beside `requests`.
  Nothing is delivered when the job fails: an event belongs to a job that finished.

### 2. Subscribing and delivery

- A subscriber stage names `plugin:<publisher>/<event>` in a trigger and the publisher in
  `[[depends]]` (54.39), and asks for `events.subscribe:<publisher>` (risk `medium`, words in
  English and Arabic, 54.08). The admin approves it in the grants dialog like any capability;
  without it the subscription is not wired and the page says so.
- The pipeline turns each allowed event into one `Request` per wired subscriber stage, with
  the payload `{"event": "<name>", "from": "<publisher id>", "payload": {…}}`, reason
  `plugin:<publisher>`. A subscriber that is disabled, held, revoked or missing gets nothing;
  events are not queued for it.
- The subscriber reads the event with `job.payload()`; the lineage is not in it.

### 3. Lineage

Each plugin job has a lineage, stored with the job (`_lineage` in `processing_jobs.payload`,
a key the pipeline reserves, strips before the guest sees the payload, and refuses from a
plugin's own request payload):

```json
{"origin": {"kind": "core", "event": "audio.ready", "subject": "video:…"},
 "hops": 2,
 "path": [["p_1a2b3c_find_silences", "video:…"], ["p_4d5e6f_write_chapters", "video:…"]]}
```

- **Origin**: a core event (a job a core edge enqueued), `schedule`, or `admin` (a catch-up
  or a retry). A job enqueued without a lineage starts one with hops 1 and its own pair.
- **Extending**: a request or an emitted event gives the new job `hops + 1` and the path plus
  `(target stage, target subject)`.
- **Through data**: every plugin write (`set-field`, tags, chapters, subtitles, artwork)
  stamps the subject in `plugin_write_lineage` (subject kind, subject id, plugin id, the
  writer's lineage, the time). A job started by a core event inherits the stamp on its
  subject when it is at most 10 minutes old.
- **Refused**: the API refuses, at `queue/request` and `events/emit`, a target whose lineage
  would have more than 16 hops or a path that already holds the `(stage, subject)` pair; and
  at the mint of a job whose inherited lineage already holds its own pair. Nothing is
  enqueued, the guest gets `call-error.scope`, the refusal is logged with the path, and the
  job ends with the cause **`plugin_loop`**: `item_permanent`, not retried; the subject's next
  real change runs the stage again. The pipeline checks the same rule when it enqueues, as a
  second line.

### 4. The cause

`plugin_loop` joins Epic 40's table (`engine/failure_classes.py`, `causes.py`, the Go
`queues/causes.go`) with its label and help in English and Arabic: "Stopped a loop between
plugins" / «أُوقفت حلقة بين الإضافات», and help that names the path from the job's detail
and says how to fix it (turn off one of the plugins on the path, or ask its developer).

## Acceptance criteria

- **Given** plugin A emitting `silences.found` and plugin B subscribed and granted,
  **when** A's stage finishes on a video,
  **then** B's stage gets one job for that video with A's payload and a lineage of 2 hops.
- **Given** B not granted `events.subscribe:A`,
  **when** A emits,
  **then** B gets nothing and its page asks for the grant.
- **Given** A emitting an event it did not declare, or a payload its schema refuses,
  **when** it calls `emit`,
  **then** the call is refused and nothing is delivered.
- **Given** two plugins that pass the static check but loop through data (A writes a tag, the
  core reruns and fires `metadata.changed`, A runs again on the same video),
  **when** the loop comes round,
  **then** A's second job fails with `plugin_loop` and nothing more is enqueued.
- **Given** a hostile plugin that requests itself on a new subject each time,
  **when** it runs,
  **then** the 17th hop is refused with `plugin_loop`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go) | emit routes | declared / undeclared / wrong subject / bad payload / 1,001st | ok / 403 / 403 / 400 / 403 |
| T02 | unit (py) | result with events | map | one request per wired subscriber, `_lineage` extended |
| T03 | integration (PG, go) | lineage at 16 hops | request | `plugin_loop` |
| T04 | integration (PG, go) | path holding the target pair | emit | `plugin_loop` |
| T05 | integration (PG, go) | write stamp 5 min old / 11 min old | mint a core-triggered job on the subject | inherited and refused / fresh lineage |
| T06 | hostile (rust guest + PG) | a guest that loops through data, one that re-requests itself, one that forges `_lineage` in its payload | run in the e2e | stopped, `plugin_loop`; forged key refused |
| T07 | e2e (`plugins-e2e`) | two-plugin chain on the hello guests | A's event | B ran on the same subject |
| T08 | unit (py, go) | `plugin_loop` | cause tables | `item_permanent`, en and ar label and help |

## Edge cases

- **An upstream disabled mid-chain**: jobs already enqueued for subscribers run; nothing new
  arrives.
- **A subscriber re-enabled later**: it does not get the events it missed; a catch-up
  (Epic 51) runs its core-triggered stages only.
- **Coalesced requests** (the same stage and subject queued twice): the newer lineage wins,
  which is the longer one when it came from the loop.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/wit/maktaba-plugin.wit` | 1.1: `events` |
| `shared/plugins/capabilities.toml` | `events.subscribe` |
| `plugind/crates/core` | link 1.0 and 1.1; forward `events/emit`; `events` in `result` |
| `api/internal/plugins/routes.go`, `tokens.go` | `events/emit`, lineage in the token, the refusals, the write stamps |
| `shared/db/migrations/` | `plugin_write_lineage` (the #693 table pattern) |
| `pipeline/src/maktaba_pipeline/plugins/dispatch.py`, `loader.py` | events to requests, `_lineage`, the second-line check |
| `pipeline/src/maktaba_pipeline/engine/failure_classes.py`, `causes.py`; `api/internal/handlers/queues/causes.go`; `web/src/i18n` | `plugin_loop` |
| `plugind/guests/hostile` | the loop guests |

### Tests

T01–T08.

### Rollout

Dark until a plugin declares `[[event]]`. Old guests keep 1.0.

### Rollback

A release without events refuses manifests with `[[event]]` (`incompatible`, never
*Failed*); `_lineage` keys left in payloads are ignored.

### Dependencies

54.04 (plugin API), 54.06 (dispatch), 54.08 (grants), 54.39 (the declared graph).
