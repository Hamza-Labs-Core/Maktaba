# Implementation Plan — Story 25.9 HTTP relay proxy

> Companion to [story-25-09-http-relay-proxy.md](story-25-09-http-relay-proxy.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Role | `maktaba-cloud --role=relay` (same binary as 25.8). Adds an HTTP/2 server on `:443` (TLS-terminated at LB; in-pod we get `:80`). |
| Subdomain → server lookup | `cloud_subdomains` table (citext PK; populated by 25.22). LRU cache 60s TTL, invalidated by `LISTEN subdomain_changed` from cloud workers. |
| Tunnel lookup | `Registry.Get(server_id)` from 25.8. |
| Stream allocation | Reuse 25.8 tunnel's `NewClientStream`. |
| WebSocket pass-through | New frame type `0x30 WS_HEAD`; bidirectional binary pump. |
| Header sanitization | Strip hop-by-hop + forwarded; rewrite `Host`; add `X-Maktaba-*` metadata. |
| Limits | 30s headers; 5min streaming; 10min download cap (overridden by tier 25.12). Body buffered ≤ 1 MiB at a time. |
| Out of scope | Tier enforcement (25.12), bandwidth metering hookpoint (25.11). This story exposes the byte-counter hook for those stories. |

## 1. Listener wiring (relay role)

```go
// cloud/internal/relay/proxy.go
type Proxy struct {
    registry *Registry           // from 25.8
    routes   *HostRouter
    sanitize *HeaderSanitizer
    meter    Meter                // 25.11 byte-counter hook (no-op stub here)
    tiers    TierGate             // 25.12 gate (no-op stub here)
    metrics  *ProxyMetrics
}

func (p *Proxy) Handler() http.Handler {
    return http.HandlerFunc(p.serve)
}
```

Wire-up in `cmd/maktaba-cloud/role_relay.go`:

```go
go (&http.Server{
    Addr:        ":8081",
    Handler:     relayServer.Handler(),            // tunnel WSS
    ReadHeaderTimeout: 5 * time.Second,
}).ListenAndServe()

go (&http.Server{
    Addr:        ":80",                            // LB terminates TLS
    Handler:     proxy.Handler(),
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:  6 * time.Hour,
    WriteTimeout: 6 * time.Hour,
}).ListenAndServe()
```

## 2. Host lookup

```go
type HostRouter struct {
    cache  *lru.Cache[string, *hostRoute]
    repo   *Repo
    pgsub  *pgsub.Subscription  // LISTEN subdomain_changed
}

type hostRoute struct {
    serverID    uuid.UUID
    userID      uuid.UUID
    suspended   bool
    serverSuspended bool
    fetchedAt   time.Time
}

func (h *HostRouter) Resolve(ctx context.Context, host string) (*hostRoute, error) {
    name, err := extractSubdomain(host)         // strip ".maktaba.app"
    if err != nil { return nil, errBadHost }
    if v, ok := h.cache.Get(strings.ToLower(name)); ok && time.Since(v.fetchedAt) < 60*time.Second {
        return v, nil
    }
    row, err := h.repo.LookupByName(ctx, name)  // joins cloud_subdomains + cloud_servers + cloud_users
    switch {
    case errors.Is(err, ErrNotFound):
        return nil, errUnknownHost
    case row.ReservedReserved:
        return nil, errReservedHost
    }
    h.cache.Add(strings.ToLower(name), row)
    return row, nil
}

func extractSubdomain(host string) (string, error) {
    host = strings.ToLower(host)
    if host == "" { return "", errBadHost }
    // Strip optional port
    if i := strings.IndexByte(host, ':'); i >= 0 { host = host[:i] }
    suffix := ".maktaba.app"
    if !strings.HasSuffix(host, suffix) { return "", errBadHost }
    name := strings.TrimSuffix(host, suffix)
    if !subdomainRegex.MatchString(name) { return "", errBadHost }
    return name, nil
}
```

`subdomainRegex = ^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$` (matches 25.22's validator).

`pgsub` subscribes to `LISTEN subdomain_changed`; payload is the subdomain name; we evict from cache.

## 3. Serve handler

```go
func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
    if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
        // Behind LB; require https.
        http.Redirect(w, r, "https://"+r.Host+r.RequestURI, 301); return
    }
    if r.Header.Get("Upgrade") == "websocket" {
        p.serveWS(w, r); return
    }
    route, err := p.routes.Resolve(r.Context(), r.Host)
    if err != nil {
        switch {
        case errors.Is(err, errBadHost):       writeProblem(w, r, 400, "bad_host")
        case errors.Is(err, errUnknownHost):   writeProblem(w, r, 404, "unknown_host")
        case errors.Is(err, errReservedHost):  writeProblem(w, r, 404, "unknown_host")
        }
        return
    }
    if route.suspended || route.serverSuspended {
        writeProblem(w, r, 503, "server_suspended"); return
    }
    tunnel, ok := p.registry.Get(route.serverID)
    if !ok {
        w.Header().Set("Retry-After", "60")
        writeProblemJSON(w, r, 503, "server_offline", map[string]any{
            "server_id": route.serverID.String(),
            "last_seen_at": p.repo.LastSeen(r.Context(), route.serverID),
        })
        return
    }
    // Tier + stream gate (25.12 hook):
    release, err := p.tiers.Acquire(r.Context(), route.userID, isStream(r))
    if err != nil { p.writeTierError(w, r, err); return }
    defer release()

    // Build REQ_HEAD payload
    head, err := p.sanitize.BuildReqHead(r, route)
    if err != nil { writeProblem(w, r, 400, "bad_request"); return }

    stream, err := tunnel.NewClientStream(r.Context(), head, r.Body)
    if err != nil { writeProblem(w, r, 503, "tunnel_full"); return }
    defer stream.Cancel()

    // Wait for headers with timeout
    selectCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
    defer cancel()
    select {
    case rh := <-stream.HeadCh():
        // Filter and write headers
        for _, kv := range rh.Headers { if !isHopByHopOutbound(kv[0]) { w.Header().Add(kv[0], kv[1]) } }
        w.Header().Set("Via", "1.1 maktaba-relay")
        w.WriteHeader(rh.Status)
    case <-selectCtx.Done():
        writeProblem(w, r, 504, "upstream_timeout"); return
    }
    // Stream body with byte-counter
    counter := p.meter.NewBodyCounter(route.serverID, route.userID, "out")
    for chunk := range stream.BodyCh() {
        counter.Add(int64(len(chunk)))
        if _, err := w.Write(chunk); err != nil { return }
        // emit WINDOW_UPDATE proportional
        tunnel.SendWindowUpdate(stream.ID(), uint32(len(chunk)))
    }
}
```

`isStream(r)` matches `/api/streams/`, `/api/videos/*/play`, `*.m3u8`, `*.mpd`, `*.ts`, `*.m4s`, HLS/DASH segment patterns.

`writeProblem` returns either JSON or HTML depending on `Accept`.

## 4. Header sanitization

```go
// cloud/internal/relay/header_sanitizer.go
func (s *HeaderSanitizer) BuildReqHead(r *http.Request, route *hostRoute) (ReqHead, error) {
    // Validate single Host header and CRLF safety
    if err := validateHeaders(r.Header); err != nil { return ReqHead{}, err }
    headers := make([][2]string, 0, len(r.Header))
    for k, vs := range r.Header {
        if isHopByHopInbound(k) { continue }
        if k == "X-Forwarded-For" || k == "Forwarded" || k == "Cf-Connecting-Ip" || k == "X-Real-Ip" { continue }
        for _, v := range vs { headers = append(headers, [2]string{k, v}) }
    }
    headers = append(headers,
        [2]string{"Host", "maktaba.local"},
        [2]string{"X-Maktaba-Original-Host", r.Host},
        [2]string{"X-Maktaba-Client-Ip", clientIP(r)},
    )
    if sub := extractJWTSub(r.Header.Get("Authorization")); sub != "" {
        headers = append(headers, [2]string{"X-Maktaba-Cloud-User", sub})
    }
    return ReqHead{
        Method: r.Method,
        Path:   r.URL.RequestURI(),
        Headers: headers,
        ClientIP: clientIP(r),
        OriginalHost: r.Host,
    }, nil
}

func validateHeaders(h http.Header) error {
    if len(h["Host"]) > 1 { return errBadRequest }
    if len(h["Content-Length"]) > 1 { return errBadRequest }
    if has(h, "Transfer-Encoding") && has(h, "Content-Length") { return errBadRequest }
    for k, vs := range h {
        if !validHeaderName(k) { return errBadRequest }
        for _, v := range vs {
            if strings.ContainsAny(v, "\r\n\x00") { return errBadRequest }
            if len(v) > 8192 { return errBadRequest }
        }
    }
    return nil
}
```

Hop-by-hop list (RFC 7230): `Connection, Keep-Alive, Proxy-Authenticate, Proxy-Authorization, TE, Trailer, Transfer-Encoding, Upgrade`.

`clientIP(r)` reads `X-Forwarded-For` only if the request came from a Cloudflare or Hetzner LB IP allow-list (25.24 will own this; here we accept the LB header).

## 5. WebSocket pass-through

```go
func (p *Proxy) serveWS(w http.ResponseWriter, r *http.Request) {
    route, err := p.routes.Resolve(r.Context(), r.Host)
    if err != nil { writeProblem(w, r, 404, "unknown_host"); return }
    tunnel, ok := p.registry.Get(route.serverID)
    if !ok { writeProblem(w, r, 503, "server_offline"); return }
    // Accept the client WS
    client, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
    if err != nil { return }
    defer client.Close(websocket.StatusInternalError, "")
    // Open a tunneled WS stream
    stream, err := tunnel.OpenWS(r.Context(), wsHeadFor(r))
    if err != nil { client.Close(websocket.StatusGoingAway, "tunnel_full"); return }
    defer stream.Cancel()
    // Bidirectional pump
    g, gctx := errgroup.WithContext(r.Context())
    g.Go(func() error { return pipeClientToTunnel(gctx, client, stream) })
    g.Go(func() error { return pipeTunnelToClient(gctx, stream, client) })
    _ = g.Wait()
}
```

`OpenWS` emits a `0x30 WS_HEAD` frame; subsequent frames carry binary payload.

## 6. Limits + timeouts

```go
type limits struct {
    headersTimeout   time.Duration // 30s
    streamingTimeout time.Duration // 5min for the whole streaming response
    downloadCap      time.Duration // 10min (Pro); 30min (Family); from tier
}
```

For HLS chunk endpoints (path matches `*.ts`, `*.m4s`), each chunk resets the `streamingTimeout`. We hold the connection until either the server closes (RESP_END) or the user disconnects.

`MaxBytesReader(r.Body, perRequestUploadCap)` — 5 GiB cap on request bodies; configurable per tier.

## 7. Metrics

```go
type ProxyMetrics struct {
    Requests   *prometheus.CounterVec   // code
    Latency    prometheus.Histogram
    BytesOut   *prometheus.CounterVec   // server_id
    BytesIn    *prometheus.CounterVec   // server_id
    TLSErrors  *prometheus.CounterVec   // reason
    WSOpen     prometheus.Gauge
    Streams    prometheus.Gauge
}
```

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestExtractSubdomain` | `mahmoud.maktaba.app` → `mahmoud`; `..maktaba.app` → error; `evil.com` → error. |
| `TestHostRegexBoundaries` | Valid + invalid pairs per 25.22. |
| `TestSanitizeStripsHopByHop` | Connection / Proxy-* / Transfer-Encoding removed. |
| `TestSanitizeRejectsSmuggle` | Duplicate CL or CL+TE → 400. |
| `TestSanitizeAddsXMaktabaHeaders` | Original-Host / Client-Ip / Cloud-User present. |
| `TestCRLFInHeaderValueRejected` | `Header: foo\r\nX-Inject: bar` → 400. |

### 8.2 Integration (mock tunnel + mock local API in 25.7)

| Test | Pins |
|---|---|
| `TestGETBytewise` | Body byte-identical to direct LAN call. |
| `TestFirstByteLatency100MB` | Streaming server: first byte ≤ 100 ms after server emits. |
| `TestUpload1GB` | Memory stays bounded; metric matches. |
| `TestConnectionClose` | Server `Connection: close` → relay closes client conn. |
| `TestSuspendedSubdomain` | 503 with billing-reference body. |
| `TestOfflineServer503` | `Retry-After: 60` + `last_seen_at` body. |
| `TestUnknownHost404` | Unclaimed name. |
| `TestMisdirectedRequest421` | Host doesn't match `*.maktaba.app` → 421. (Edge LB normally rejects; relay defensive 400.) |
| `TestWSUpgradePassthrough` | `/ws/library/{id}` bidirectional. |
| `TestRangeRequest206` | Range header forwarded; partial content returned. |

### 8.3 Regression

| Test | Pins |
|---|---|
| `TestNullByteInPath` | `/path%00x` → 400. |
| `TestHostInjection` | `Host: a..b.maktaba.app` → 400. |
| `TestEmbeddedCRLF` | Header value with `\r\n` → 400. |
| `TestPipeliningGracefullyHandled` | HTTP/1.1 pipelining (rare) → safe. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| HTTP request smuggling | Strict header validation. | `TestSanitizeRejectsSmuggle`. |
| Hop-by-hop forwarded | Stripped. | `TestSanitizeStripsHopByHop`. |
| Range requests | Pass through unchanged. | `TestRangeRequest206`. |
| Long polls | Not used by Maktaba endpoints; documented; future `X-Long-Poll` header switch. | Doc. |
| Slow client | Tunnel sees window-narrow; relay's response writer applies backpressure. | Cross-test. |
| WSS upgrade rate | Capped at proxy (25.24 owns numbers). | Cross-test 25.24. |
| TLS handshake errors | Counted; no listener crash. | `TLSErrors` metric. |
| DDoS streaming | Cloudflare in front of `app./api./admin.`; streaming subdomains bypass CF for cost — protected via 25.24 + per-server circuit breaker. | Cross-story 25.25. |
| HEAD | Forwarded; body frames absent. | `TestHEAD`. |
| Trailers | Forwarded. | `TestTrailers`. |
| Per-server stream cap | Default 200 streams/server; 25.12 cap overrides. | Spec, Implementation. |
| TLS pinning | We do **not** ship pinning to clients (cloud in trust boundary). | Cross-story 25.23. |
| HTTP/3 | Out for v1. | Spec. |

## 10. Dependencies

- 25.1, 25.6, 25.7, 25.8.
- 25.11 (bandwidth meter hook stub).
- 25.12 (tier gate hook stub).
- 25.22 (subdomain table; this story stub-creates a row for tests via fixtures).
- 25.23 (wildcard TLS at LB).
- 25.24 (rate limit shape).

## 11. Acceptance checklist

- [ ] `Proxy.serve` routes `*.maktaba.app` through tunnel.
- [ ] Subdomain LRU cache + LISTEN invalidation.
- [ ] Header sanitization + smuggling defenses.
- [ ] WS pass-through bidirectional.
- [ ] 30s headers, 5min streaming, 10min download timeouts.
- [ ] Suspended/offline/unknown responses match spec.
- [ ] Metrics expose Requests, BytesOut/In, TLSErrors.
- [ ] Tests in §8 pass.
