# Story 25.10 — Direct-connection probe & LAN fallback

> Epic 25 · Cloud relay · Phase 2 (relay)

## Description

The relay is the fallback path, not the default. When a client and a
server are on the same LAN (or otherwise reachable directly), the
client should bypass the cloud and talk to the server directly. This
saves user bandwidth, reduces our cost, and cuts latency from ~80ms to
~3ms. This story implements client-side probing.

Algorithm (in client code, all platforms):

1. **Resolve.** On app start (and every 5 min while foreground), the
   client asks the cloud: `GET /api/servers/{server_id}/endpoints`.
   The cloud returns:
   ```json
   {
     "lan": [
       { "url": "http://192.168.1.42:8080", "source": "mdns" },
       { "url": "http://10.0.0.5:8080",   "source": "user-set" }
     ],
     "relay": "https://mahmoud.maktaba.app",
     "preferred": "lan"
   }
   ```
   The cloud knows the LAN candidates because mDNS-discovered
   endpoints (Epic 15.2) are reported by the server in tunnel
   metadata, **not** because the cloud sniffs the LAN.

2. **Probe.** For each LAN candidate, race a `GET /api/health` with
   a 1-second timeout. The first to return 200 wins. If all fail,
   fall back to relay.

3. **Pin.** The chosen base URL is cached for 5 minutes per
   `(client, server_id)`; on network change events
   (NSNotificationCenter / ConnectivityManager / `online`/`offline`),
   the cache is invalidated immediately.

4. **Stream affinity.** A live HLS playback that started on LAN
   stays on LAN for its session even if relay later becomes
   "preferred". Switching mid-stream causes a stutter.

Server-side endpoint discovery:

- The server reports its LAN candidates as a tunnel metadata frame
  (`0x40 META_ENDPOINTS`) on connect and on network-change.
- Sources: mDNS-published `_maktaba._tcp.local.`, manually
  configured static IPs, IPv6 link-local addresses (when
  applicable).
- The cloud filters out RFC 1918 IPs that *appear* unique-globally
  weird, but does not validate reachability — that's the client's
  job.

Privacy:

- LAN candidate IPs are private addresses; we store them
  encrypted-at-rest and never log them outside the user's own
  audit feed.
- We never expose another user's LAN IPs.

## Acceptance criteria

- **Given** a client on the same LAN as the server,
  **when** the client probes,
  **then** at least one LAN candidate returns 200 within 1s and
  is pinned for 5 min.
- **Given** a client off-LAN,
  **when** all LAN probes fail or time out,
  **then** the client falls back to `https://mahmoud.maktaba.app`
  and `cloud_streams_active` accounts the stream.
- **Given** the client is on the LAN but the server is offline,
  **when** the client probes,
  **then** all probes fail and the client falls back to relay,
  which returns `503 server_offline`. The client surfaces a
  graceful error.
- **Given** the user changes Wi-Fi networks,
  **when** the OS emits a connectivity-change event,
  **then** the client invalidates the pinned base URL and
  re-probes.
- **Given** a probe returns a `200` but with a different
  `X-Maktaba-Server-Id` header than expected (e.g., DNS
  rebinding to attacker),
  **when** the client checks the header,
  **then** the candidate is rejected and `cloud_abuse_events`
  records `kind=lan_probe_id_mismatch` (reported by the next
  cloud call).
- **Given** the cloud has no recorded LAN candidates,
  **when** the client probes,
  **then** the client immediately uses the relay; no probe
  attempts.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | mock LAN candidate replies 200 in 50ms | probe | LAN chosen |
| T02 | integration | LAN candidates time out at 2s | probe | relay chosen at 1s |
| T03 | integration | two LAN candidates, both healthy | probe | first to respond wins |
| T04 | regression  | LAN candidate returns wrong `server_id` | probe | rejected, abuse event queued |
| T05 | unit        | base URL pin TTL 5 min | clock advance 6 min | re-probe triggered |
| T06 | integration | mid-stream Wi-Fi switch | observe HLS | session stays on its original base URL until end |
| T07 | regression  | TLS LAN candidate (caddy local CA) | probe | accepted only if the OS trusts the local CA, else falls back |
| T08 | integration | privacy: list endpoints for *another* user | API | 403 not_your_server |
| T09 | unit        | endpoint cache eviction on connectivity change | flag toggled | cache cleared |
| T10 | regression  | mDNS-discovered candidate has private hostname | probe | hostname-based candidates require user opt-in (avoid SSRF-via-LAN-hostname) |

## Edge cases

- **Tailscale / VPN.** A user on Tailscale may have a
  100.x.y.z direct route. The server's mDNS announcement
  doesn't include it, but the user can add a manual
  override in app settings: "I have a direct route to my
  server at <ip>". Client probes the override first.
- **VPN-induced reachability.** A user on a corporate VPN
  may unexpectedly reach LAN candidates that aren't really
  local. That's fine — direct is direct.
- **CGNAT.** Mobile data may route the client through CGNAT
  IPs that look private but aren't. Probes will simply fail
  (unless improbable luck) and we fall back to relay.
- **HTTPS to private IP.** Browser security policies on web
  block mixed content; if the page is `https://app.maktaba.app`,
  it cannot fetch `http://192.168.1.42`. Web client must
  either (a) only run direct on `http://localhost`, or (b)
  use Caddy local-CA HTTPS. We document (b) as the
  recommended path; the desktop and mobile apps don't have
  this constraint.
- **Stable LAN identifier.** The client uses
  `(SSID, gateway-MAC)` as a "network fingerprint" to skip
  probes when nothing changed; on iOS we have CNCopyCurrentNetworkInfo
  with location permission only — without it, we always
  probe. Document the trade-off.
- **Server moves IP.** mDNS catches it; server posts new
  META_ENDPOINTS; client's next probe finds new IP.
- **Probe timing budget.** 1s parallel probe; 2s sequential
  upper bound. We must not block app startup waiting; the
  decision is async, with a fallback to relay if probing
  hasn't completed by 1.5s and the user issued a request.
- **Logging in client.** Probe outcomes log to local file
  only; *never* to cloud. Telemetry (Epic 16.5) opt-in
  may report aggregate counts (X% of sessions on LAN).
- **Same-pod routing for HLS bursts.** Once a session is
  pinned to relay, all manifest+chunk requests use the
  same base URL until the session ends. Defined in
  client-side player wrapper.

## Files / packages

- Client web: `web/src/lib/server-endpoint.ts`.
- iOS / Android (Capacitor plugin):
  `mobile/plugins/maktaba-net/`.
- Desktop (Tauri): `desktop/src-tauri/src/net.rs`.
- TV (native): `tv/ios/Sources/MaktabaNet/`,
  `tv/android/app/src/main/kotlin/.../net/`.
- Cloud: `cloud/internal/server/endpoints.go`.

## Open questions

- **WebRTC NAT punching.** Could remove relay cost for a
  large fraction of off-LAN sessions. v2; out for v1.
- **STUN-server based hole-punching.** Same; would let us
  disintermediate from the relay path entirely. Defer.
