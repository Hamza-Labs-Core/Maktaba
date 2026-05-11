# Implementation Plan — Story 25.16 Server status dashboard

> Companion to [story-25-16-server-status-dashboard.md](story-25-16-server-status-dashboard.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoints | `GET /api/servers`, `GET /api/servers/{id}`, `GET /api/servers/{id}/status`, `GET /api/servers/{id}/usage`, `DELETE /api/servers/{id}`, `POST /api/servers/{id}/reconnect-hint`. WS `/ws/servers`. |
| Online/offline | `online = last_seen_at >= now()-60s AND registry.Has(id)`. Cross-checks the live tunnel registry via inter-pod RPC (250ms deadline). |
| Realtime | Server-Sent Events fallback if WS not desired; v1 uses WS only. WS auth: JWT in `Sec-WebSocket-Protocol` header (cloud convention). |
| Release manifest poll | Cron every 6h hits `https://releases.maktaba.app/manifest.json`; compares each server's version to `latest_stable`. |
| Unlink | Closes tunnel, revokes bearer, releases subdomain (grace), drops server row (cascade). |
| Out of scope | Server logs surfacing (deferred v2). Auto-update orchestration (25.34). |

## 1. List + detail

```go
// GET /api/servers
func list(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := currentUserID(r)
        servers, _ := s.repo.ListServersForUser(r.Context(), uid)
        // Annotate online by asking the per-pod registry; falls back to DB last_seen_at on RPC timeout.
        ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
        defer cancel()
        for i := range servers {
            servers[i].Online = s.registry.IsOnline(ctx, servers[i].ID, servers[i].LastSeenAt)
        }
        writeJSON(w, 200, servers)
    }
}
```

`registry.IsOnline` checks the local pod's `sync.Map`; for multi-pod it multicasts to peers via a tiny internal HTTP endpoint `POST /internal/registry/has` (with mTLS in the cluster network). On any error or timeout, falls back to `last_seen_at >= now()-60s`.

## 2. Status snapshot

```go
// GET /api/servers/{id}/status
type StatusResp struct {
    ID               uuid.UUID `json:"id"`
    Online           bool      `json:"online"`
    LastSeenAt       time.Time `json:"last_seen_at"`
    Version          string    `json:"version"`
    UpdateAvailable  *UpdateHint `json:"update_available,omitempty"`
    Subdomain        string    `json:"subdomain"`
    StreamsInFlight  int       `json:"streams_in_flight"`
    BandwidthMonth   int64     `json:"bandwidth_month_bytes"`
    LastError        string    `json:"last_error,omitempty"`
    TunnelUncertain  bool      `json:"tunnel_uncertain,omitempty"`
}
```

`tunnel_uncertain=true` when the registry multicast timed out and we're returning DB data only.

## 3. `DELETE /api/servers/{id}` (unlink)

Transaction:

1. Validate ownership.
2. Mark tunnel close: `tunnel := registry.Get(id); if tunnel != nil { tunnel.Send(FrameRevoke, …); tunnel.Close(4000) }`.
3. `UPDATE cloud_servers SET deleted_at=now() WHERE id=$1` (soft delete preserves audit).
4. `UPDATE cloud_server_tokens SET revoked_at=now() WHERE server_id=$1 AND revoked_at IS NULL`.
5. `UPDATE cloud_subdomains SET released_at=now(), redirect_until=now()+30d WHERE server_id=$1`.
6. Audit `server.unlink`.
7. `pg_notify('subdomain_changed', subdomain_name)` to evict relay cache.

Bearer cache (25.8): admin path calls `registry.PurgeBearer(serverID)` so a stale cache entry can't accept the old token.

## 4. Reconnect hint

```go
// POST /api/servers/{id}/reconnect-hint
func reconnectHint(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // rate-limit: 1/hour/server.
        if blocked := s.rl.Check(r.Context(), "reconnect-hint:"+id.String(), 1, time.Hour); blocked {
            w.Header().Set("Retry-After", "3600"); problem(w, 429, "throttled", ""); return
        }
        s.push.Dispatch(r.Context(), &PushReq{
            UserID: uid, Kind: "system.alert",
            Data: map[string]any{"server_id": id.String(), "title": "Server seems offline"},
        })
        writeJSON(w, 202, map[string]string{"status":"sent"})
    }
}
```

## 5. Realtime `/ws/servers`

```go
func wsServers(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := currentUserID(r)
        ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"maktaba.cloud.v1"}})
        if err != nil { return }
        defer ws.Close(websocket.StatusInternalError, "")
        ch := s.bus.Subscribe(uid)         // bus emits server.online / server.offline / server.usage_tick
        defer s.bus.Unsubscribe(uid, ch)
        for ev := range ch {
            ws.Write(r.Context(), websocket.MessageText, mustJSON(ev))
        }
    }
}
```

The 25.8 registry's `OnConnect`/`OnDisconnect` observer publishes onto the bus. Debouncing (≤10s flap) is at the **client** per spec.

## 6. Release-version check

```go
// cloud/internal/jobs/check_releases.go
func CheckReleases(ctx context.Context, db *pgxpool.Pool) error {
    body, _ := http.Get("https://releases.maktaba.app/manifest.json")
    var m Manifest; json.NewDecoder(body.Body).Decode(&m)
    stable := m.Channels["stable"].Version
    _, err := db.Exec(ctx, `
        UPDATE cloud_servers SET update_available = CASE
            WHEN semver_lt(version, $1) THEN $1
            ELSE NULL
        END WHERE deleted_at IS NULL
    `, stable)
    return err
}
```

Semver compare lives as a Postgres function `semver_lt(text,text)` added in `00060001_admin_revenue.sql` (or stub via `CASE` on lexicographic compare with caveats — better to use Go-side per-row update).

## 7. UI

```tsx
// web/src/pages/Servers.tsx
export function Servers() {
    const servers = useQuery(["servers"], fetchServers, {refetchInterval: 30_000});
    useServerEvents();  // WS subscriber
    return (
        <Grid>
            {servers.data?.map(s => <ServerCard server={s}/>)}
            {servers.data?.length === 0 && <EmptyState/>}
        </Grid>
    );
}
```

`ServerCard`:

```tsx
<article className="card">
  <Indicator state={s.online ? "online" : "offline"}/>
  <h3>{s.subdomain}.maktaba.app</h3>
  <p>{relativeTime(s.last_seen_at)}</p>
  <UsageBar usedGB={...} capGB={...} />
  {s.update_available && <Badge>Update available: {s.update_available.version}</Badge>}
  <Menu>
    <MenuItem onClick={...}>Reconnect hint</MenuItem>
    <MenuItem destructive onClick={confirmUnlink}>Unlink…</MenuItem>
  </Menu>
</article>
```

Indicator colours:

- green = online + last_seen ≤ 60s.
- amber = recently seen (60-300s) → reconnecting.
- gray = offline (> 5 min).

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestOnlineComputation` | last_seen_at + registry → state. |
| `TestVersionCompareSemver` | `0.6.1 < 0.7.0`. |
| `TestRateLimitReconnectHint` | 1/hour/server. |

### 8.2 Integration

| Test | Pins |
|---|---|
| `TestListAnnotatesOnline` | Two servers, one online → flags correct. |
| `TestWSEmitsOfflineOnTunnelDrop` | Disconnect → event within 1s. |
| `TestUnlinkClosesTunnel` | Tunnel disconnect within 5s; cache purged; subdomain → grace. |
| `TestUnlinkRevokesBearer` | Re-handshake with old token → 401 + abuse event. |
| `TestUpdateAvailableBadge` | Old version + new manifest → badge appears. |
| `TestUsagePagination` | 90+ days requests paginate. |
| `TestForbidsOtherUserServer` | 404 on someone else's id. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Flapping tunnel | Server side broadcasts; client debounces 10s. | UX. |
| Clock drift | UTC at API; relative rendered client-side. | Doc. |
| Subdomain after unlink | 30d grace 301-redirect; static page. | Cross-25.22. |
| Server lies about version | Display + low-confidence tag if not in our manifest's known hashes. | Spec. |
| Multiple offline at once | Rate-limit hint to 1/hour/server. | `TestRateLimitReconnectHint`. |
| Live unlink mid-stream | Confirmation dialog warns; in-flight gets 502. | UX. |
| Breaking-release flag | Badge text changes to "Major update — read notes". | UI. |
| Usage chart > 90d | Pagination via opaque cursor. | API. |
| Reconnect hint when push disabled | Push delivery best-effort; no API error. | `TestReconnectHintFallback`. |
| RPC timeout | DB fallback; `tunnel_uncertain=true`. | `TestRegistryRPCTimeout`. |

## 10. Dependencies

- 25.1, 25.6, 25.7, 25.8 (registry observer).
- 25.11 (usage).
- 25.17 (push for reconnect hint).
- 25.22 (subdomain release).
- 25.34 (release manifest schema).

## 11. Acceptance checklist

- [ ] All 6 REST endpoints + `/ws/servers` implemented.
- [ ] Online state cross-checks registry with 250ms RPC.
- [ ] Unlink: tunnel close + bearer revoke + subdomain grace.
- [ ] Update-available cron every 6h.
- [ ] WS emits `server.online` / `server.offline` on registry events.
- [ ] Tests in §8 pass.
