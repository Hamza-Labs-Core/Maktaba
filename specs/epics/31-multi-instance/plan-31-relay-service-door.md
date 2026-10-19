# Plan 31.R — The service door — implementation

> Implementation plan for [story-31-relay-service-door](story-31-relay-service-door.md).
> **No migration**: the settings are a rules section (`app_settings` key `rules.relay`), the
> audit goes to the hub's events file, the secret is read from `home_secrets` (slot 0120).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The calls travel on the existing agent channel (new frames `svc.*`), not a new HTTPS endpoint. | The channel already authenticates both sides (handshake v2), seals every frame, knows which node a session is, and closes on revocation. An endpoint would need its own node credentials. |
| D2 | The node sends method + path + query only; the home owns the upstream address (the extension's row) and the secret. | No URL from a node can ever be fetched by the home (no SSRF); the allow-list matches exactly what Plex will see. |
| D3 | The allow-list is per extension, in Go (`api/internal/cluster/relay/allow.go`): anchored path patterns, allowed query names with value patterns, required names, fixed values; DELETE is verified to name a collection first. | Enumerated from `PlexClient` as the node-placed stages use it; a compromised node can read the Plex library and manage collections, nothing else (no prefs, no item edits, no item deletion). |
| D4 | The answer is streamed (512 KiB chunks) through a scrubber that holds back a tail, so a split token is still found; only `Content-Type` comes back; redirects are not followed. | A section listing can be tens of MB; Plex answers may carry `X-Plex-Token=` in thumb URLs; a redirect would carry the token header elsewhere. |
| D5 | Placement stays `home` with `StageDef.relay = "plex"`; `lifted_placements` lifts those stages on a node while `rules.relay.enabled` and the agent runs. | Same mechanism as the shared-model stages (`model_rules`); off restores PR #73's behaviour exactly; the home's stage registry (`job_stages.placement`) does not change. |
| D6 | Settings are the rules section `relay` (Python `RelayConfig`, schema regenerated); Go reads them with `rules.Effective`. | Rules-as-settings: one source of defaults, the settings page and PATCH for free. |
| D7 | Membership is checked per call (the hub's session is live, the registry says approved, not revoked after the session began). | A revoke is effective at the next call, even before the hub closes the session. |
| D8 | Cloudflare Stream and dizqueTV are listed as never relayed (`relay.HomeOnly`). | Cloudflare's token is for admin operations the home's API makes itself; dizqueTV programmes embed the Plex token, so those stages need it in the clear. |
| D9 | Audit = events file (`event: "relay"`) + 200 recent calls and per-machine counters in memory, shown on the Machines page. | Durable on the share for the retention days, archived on the home, no new table. |

## 1. Files

**Create**
- `api/internal/cluster/relay/allow.go` — `Extension`, `Rule`, `Plex`, `HomeOnly`, `Match`
- `api/internal/cluster/relay/scrub.go` — the streaming `Scrubber`
- `api/internal/cluster/relay/door.go` — `Door.Serve` (checks, limits, upstream call, stream,
  audit), `Status`, `Settings` (rules section), caches (5 s)
- `api/internal/cluster/relay/db.go` — `DBSettings` (rules.relay), `DBUpstream` (extension row +
  `home_secrets`)
- `api/internal/cluster/agent/relay.go` — `svc.request` / `svc.cancel` handling, the chunk sink
  (`sendWait`: waits while the session queue is half full), `relayMember`, `relayAudit`,
  `GET /api/cluster/relay`
- `api/internal/cluster/agent/relayfixture/main.go` — the home side of the two-process test
- `pipeline/src/maktaba_pipeline/relay.py` — `RelayConfig` (rules section), `RelayTransport`
  (a `plex.Transport` over the channel, retries), `attach` / `detach`, `plex_client`
- `web/src/components/cluster/RelayPanel.tsx` — the Machines page's Relay tab
- tests: `api/internal/cluster/relay/relay_test.go`, `api/internal/cluster/agent/relay_test.go`,
  `pipeline/tests/cluster/agent/test_relay_channel.py`, `pipeline/tests/cluster/test_relay_settings.py`,
  `pipeline/tests/cluster/test_relay_two_process.py`, `web/src/components/cluster/RelayPanel.test.tsx`

**Modify**
- `api/internal/cluster/agent/{hub,frames,session,mount}.go` — `Config.Relay`, the frames, the route
- `api/cluster_bootstrap.go`, `api/main.go` — the door wired with the home's secrets store
- `api/internal/handlers/rules/rules.go` (`Effective`), `schema.json` (regenerated)
- `pipeline/src/maktaba_pipeline/cluster/agent/client.py` — `relay_available`, `relay_request`,
  dispatch of `svc.chunk` / `svc.done`; `service.py` attaches the relay with the LLM door
- `pipeline/src/maktaba_pipeline/engine/graph.py` — `StageDef.relay`
- `pipeline/src/maktaba_pipeline/cluster/stages.py` — `lifted_placements` lifts relayed stages
- `pipeline/src/maktaba_pipeline/extensions/plex/__init__.py` — default client via
  `relay.plex_client`; `relay="plex"` on index, collection, groups
- `pipeline/src/maktaba_pipeline/rules.py` — the `relay` section
- `web/src/pages/Admin/Machines.tsx`, `web/src/lib/clusterApi.ts`, `web/src/i18n/{en,ar}.json`,
  `web/src/styles/cluster.css`
- docs: `design-agent.md` §5.1 and §6.7, `docs/cluster/protocol.md`, `docs/cluster/README.md`,
  story 32.6 AC6a note, `CHANGELOG.md`

## 2. Sequence (one call)

1. A node's stage calls `PlexClient` in a worker thread; its transport is `RelayTransport`
   (no token in the settings on a node). It waits up to 15 s for the channel, then schedules
   `ChannelAgent.relay_request` on the agent's loop and blocks on the result.
2. The agent sends `svc.request`; frames for that id are queued as they arrive.
3. The hub starts a goroutine per call (at most 32 per session), with a context that ends with
   the session or on `svc.cancel`.
4. `Door.Serve`: settings (cached 5 s) → home-only / unknown → membership → body size →
   allow-list → rate (wait ≤ 5 s) → slot (wait ≤ 15 s) → upstream (row + secret, cached 5 s) →
   collection check for DELETE → the call with the token header → stream through the scrubber
   in chunks → result. Every exit is recorded (`record` → ring, counters, `Audit`).
5. The hub always ends with `svc.done`; the agent returns `(status, content_type, body)`, or
   raises `RelayError` (refusals become 403 for the Plex client; `rate_limited` / `busy` are
   retried; others become a retryable `TransportError`).

## 3. Tests

As listed in the story (TC1–TC8). The two-process test builds `relayfixture` with `go build`
and needs `MAKTABA_TEST_PG_URL`; without either it is skipped.
