# Story 31.R — The home relays extension calls for machines (service door)

## Description

*As the admin, I want the machines that joined my home to run the Plex stages again — index
the libraries, write the season, topic and clip collections — without any of them ever
holding the Plex token.*

Story 32.6 (PR #73) moved every extension secret into `home_secrets` (AES-GCM, key file on the
home, revoked from `maktaba_worker`). A node can no longer read the Plex token, so the Plex
stages became home-only. The cluster channel (Epic 31 phase 2) already joins every worker to a
hub inside the home's API over an authenticated, sealed WebSocket, and already carries a
"door" (the OpenAI-compatible LLM door). This story adds a second door on the same channel:
the **service door**. A node's Plex client sends each HTTP exchange to the home as a method
plus a path; the home checks the machine and the call, adds the token, calls Plex and streams
the answer back with the token scrubbed out. The token never leaves the home.

Design: [design-agent.md](design-agent.md) §5.1 (frames `svc.*`), §6.7 (REST); plan:
[plan-31-relay-service-door.md](plan-31-relay-service-door.md). Context: Epic 32 README Q7,
[story 32.6](../32-tv-streaming-cluster-remote/story-32-06-cloudflare-integration.md) AC6a.

## How it works

```
node worker                         home API (hub)                        Plex
PlexClient ──RelayTransport──► svc.request {id, ext:"plex", method, path}
  (no token, no URL)             │ approved node? live session? not revoked?
                                 │ rules.relay.enabled?
                                 │ method + path + query on the allow-list?
                                 │ rate / in-flight limits for this machine
                                 │ + X-Plex-Token (home_secrets)  ─────────► GET /library/...
                                 │ ◄──────── answer (redirects never followed)
                                 │ scrub X-Plex-Token=… and the token itself
           ◄── svc.chunk… svc.done {status, content_type, bytes, error?}
                                 └ audit: events file + recent calls (node, method, path, status)
```

## Acceptance criteria

- **AC1** Frames on the existing channel (additive, protocol v2 unchanged): `svc.request`
  (C→S: `id`, `ext`, `method`, `path` = path and query only, `accept?`, `content_type?`,
  `body?` base64, `timeout_ms?`), `svc.chunk` (S→C: `id`, `data` base64, in order), `svc.done`
  (S→C, always last: `id`, `status`, `content_type`, `bytes`, `error?` {`code`, `detail`}),
  `svc.cancel` (C→S). No URL, host or secret travels from the node.
- **AC2** The home refuses, without calling the server: a session that is neither one of the
  home's own processes nor a node approved now (`not_member`; the registry is read per call, so
  a revoked node is refused even before its session closes); an extension that is not relayable (`unknown_extension`) or home-only
  (`home_only`: Cloudflare Stream, dizqueTV); a call not on the extension's allow-list
  (`not_allowed`); a malformed target (`bad_request`: escapes, dot segments, doubled slashes,
  fragments, absolute URLs); a request that names the secret parameter (`X-Plex-Token` in the
  query); a body over 64 KiB (`too_large`); the relay switched off (`relay_off`); the extension
  off or without a token on the home (`not_configured`).
- **AC3** The Plex allow-list is the calls the node-placed stages make (method + anchored path
  pattern + allowed query parameters with value patterns, each once, required ones present):
  see the table below. `DELETE /library/metadata/{key}` is forwarded only after the home has
  checked (with its token) that the key names a collection. Live TV paths are absent:
  `plex_livetv` stays on the home beside `write_channels`.
- **AC4** The home adds `X-Plex-Token` from `home_secrets` (`ext.plex.token`) and forwards to the
  Plex URL in the extension's row; the answer is streamed back in chunks; only the content type
  is passed back (no other header: no `Set-Cookie`, no `Location`); redirects are never
  followed; every `X-Plex-Token=…` parameter (case-insensitive, `=` or `%3D`) and every literal,
  query- or path-escaped copy of the token is removed from the body, also across chunk
  boundaries.
- **AC5** Limits per machine, from the rules section `relay` (defaults: on, 600 calls a minute,
  bursts of 60, 4 at once, answers up to 256 MB, 120 s per call): over the rate for more than
  5 s → `rate_limited`; no free slot within 15 s → `busy`; an answer over the size → `too_large`;
  past the time limit → `timeout`. The node's client retries `rate_limited` / `busy` three times
  (1 s, 2 s, 4 s) before failing the job retryably.
- **AC6** Every call is audited: the hub's events file (`event: "relay"`: session, node, worker,
  IP, extension, method, path without the query, status, bytes, ms, error code, how many
  secrets were scrubbed) and the last 200 calls with per-machine counters in memory. Never the
  token, never the query.
- **AC7** Placement: `plex_index`, `plex_collection` and `plex_groups` keep `placement="home"`
  and name `relay="plex"`; a node lifts them (as it lifts a shared-model stage) while
  `rules.relay.enabled` is on and the worker runs the agent. Off, they are home-only again
  within the node's 30 s refresh and the home refuses relayed calls. With a token (the home)
  the stages call Plex directly, as before.
- **AC8** Admin → Machines → **Relay**: the door's state and switch (`PATCH /api/rules/relay`),
  the limits, each relayable extension (ready or why not, the server's host, the allow-list),
  the never-relayed extensions with why, and the recent calls (time, machine, call, result,
  size, ms), in English and Arabic. `GET /api/cluster/relay` is admin-only and never carries a
  secret.
- **AC9** Cloudflare Stream stays home-only: its API token can create and delete every video of
  the account, and every call is an admin operation the home's API makes itself
  (`api/internal/tvremote`); no stage needs it on a node.

## The Plex allow-list

| Method | Path | Query | Client call |
|---|---|---|---|
| GET | `/identity` | — | `ping`, `machine_identifier` |
| GET | `/library/sections` | — | `sections` |
| GET | `/library/sections/{key}/all` | `type` ∈ {1, 4} | `section_listing` |
| GET | `/library/sections/{key}/collections` | — | `collections` |
| GET | `/library/metadata/{keys}` (1–50, comma) | — | `metadata`, `item_section`, `collection_labels` |
| GET | `/library/collections/{key}/children` | — | `collection_children` |
| POST | `/library/collections` | `type`=1, `title`, `smart`=0, `sectionId`, `uri`=`server://<machine>/com.plexapp.plugins.library/library/metadata/<keys>` (all required) | `create_collection` |
| PUT | `/library/collections/{key}/items` | `uri` (as above, required) | `create_collection` (members) |
| PUT | `/library/collections/{key}/items/{item}/move` | `after`? | `create_collection` (order) |
| PUT | `/library/metadata/{key}/prefs` | `collectionSort`=2 (required) | `create_collection` (custom order) |
| PUT | `/library/sections/{key}/all` | `type`=18, `id`, `label[0].tag.tag` (required), `label.locked`? | `add_label` (collections only: type 18) |
| DELETE | `/library/metadata/{key}` | — | `delete_metadata`, only when the key is a collection |

Keys are 1–12 digits. Anything else — `/:/prefs`, `/system`, `/accounts`, a library refresh,
editing an item's fields, a smart collection, a member URI to another server — is refused.

## Test cases

- **TC1** `relay.TestAllowListAcceptsTheStagesCalls` / `TestAllowListRefusals` (Go): every call
  above passes; 30+ refusals (other paths and methods, extra or repeated parameters, wrong fixed
  values, the token parameter, escapes, dot segments, absolute URLs, Live TV).
- **TC2** `TestServeInjectsTheTokenAndNeverReturnsIt`: the fake Plex receives the token header;
  the answer, the audit record and the status carry no token; thumb URLs are rewritten and the
  JSON stays valid; only the content type comes back. `TestScrubberAcrossChunkBoundaries`,
  `TestRedirectIsNotFollowed`.
- **TC3** `TestRefusals` (relay off, home-only, unknown, not a member, no token, extension off,
  body too large: none reaches Plex), `TestDeleteOnlyRemovesCollections`.
- **TC4** `TestRateLimitPerMachine` (a second machine keeps its own budget),
  `TestInFlightCap`, `TestAnswerSizeLimitAndTimeout`.
- **TC5** Hub: `TestRelayThroughTheChannel` (sealed frames, chunks, admin-only status),
  `TestRelayRefusesARevokedNode` (registry revoked, session still open), `TestRelayWithoutDoor`.
- **TC6** Python: `test_relay_channel.py` (no token and no URL in the frame, reassembly, retries,
  403 on refusal, channel lost → retryable), `test_relay_settings.py` (client choice, the rules
  section, lifting on / off / without agent / unreadable settings),
  `test_extension_graph.py` (which stages name the relay).
- **TC7** Two processes against the scratch Postgres (`test_relay_two_process.py`): the real Go
  hub and door (`relayfixture`) and this process as an approved node with no secrets key run
  `plex_index` and `plex_collection` (create, then reorder: DELETE of the old collection, then
  POST) against a fake Plex over HTTP; Plex saw the token on every call, the node and the job
  history never; then a call off the allow-list (403), the relay switched off (`relay_off`, the
  stages home-only again) and the node revoked in the registry (`not_member`).
- **TC8** Web: `RelayPanel.test.tsx` (status, allow-list, calls, the switch, no door, Arabic,
  axe).

## Edge cases

- The channel drops mid-call: the node's call fails with a retryable `plex_unreachable`; the
  hub cancels the upstream call with the session.
- The home restarts: the node's agent reconnects; calls in the meantime wait up to 15 s for the
  channel, then fail retryably.
- A huge library listing: streamed in 512 KiB chunks; the hub waits while the session's queue is
  half full, so pings still pass and the session is never closed as a slow consumer.
- The settings row is unreadable: the home uses the defaults (on); the node lifts nothing (the
  stages stay home-only).
- A token in Plex's own answers (thumb or stream URLs): removed before anything leaves the home.

## Out of scope

- dizqueTV stages on nodes: their programmes embed the Plex token, so they need it in the clear.
- Relaying Live TV (`plex_livetv`), and any other extension until it is added to `Extensions`
  with its own allow-list.
- A persistent audit table: the events file on the share (14 days, archived on the home) and the
  in-memory recent calls are the record; a database table would need a migration (none here).
