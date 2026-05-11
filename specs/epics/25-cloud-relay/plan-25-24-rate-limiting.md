# Implementation Plan — Story 25.24 Rate limiting & quota

> Companion to [story-25-24-rate-limiting.md](story-25-24-rate-limiting.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Algorithm | Sliding-window via Redis Lua: keys per (scope, actor, minute-bucket); on hit, INCR current minute, SUM windowed buckets, return remaining. |
| Storage | Redis (Sentinel HA in prod). Failure mode: **fail-closed** (`503 dependency_down`). |
| Middleware | `cloud/internal/ratelimit/middleware.go`; policies in `policies.go` map route → limit. |
| Overrides | DB-backed `rate_overrides(scope, actor, limit_per_window, window_secs, expires_at)`. |
| Stripe webhook | Bypass; signature-only auth. |
| Headers | `Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`. |
| Out of scope | CAPTCHA (deferred). |

## 1. Migration `00090002_rate_overrides.sql` (slot 0009 sub-sequence)

Lives alongside abuse signals (slot 0009 is owned by plan-25-25);
this is the sequence-2 file in the same slot.

```sql
-- +goose Up
CREATE TABLE rate_overrides (
    scope            TEXT NOT NULL,           -- 'push_per_server', 'auth_login_per_ip_email', ...
    actor            TEXT NOT NULL,           -- hashed actor id, e.g. server_id, hash(ip)
    limit_per_window INTEGER NOT NULL,
    window_secs      INTEGER NOT NULL,
    reason           TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ,
    PRIMARY KEY (scope, actor)
);

-- +goose Down
DROP TABLE IF EXISTS rate_overrides;
```

## 2. Lua atomic INCR

```lua
-- bucket_key = rl:{scope}:{actor}:{ts_minute}
-- KEYS = {bucket_key, prev_bucket_key, prev2_bucket_key, ...}  (last N minutes)
-- ARGV = {limit, ttl}
local sum = 0
for i = 1, #KEYS do
  sum = sum + (tonumber(redis.call("GET", KEYS[i])) or 0)
end
if sum >= tonumber(ARGV[1]) then
  return {sum, 0}
end
local cur = redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
return {sum + 1, 1}
```

Returns `{count_after, was_allowed}`. The `ttl` is `window_secs + 60` so a bucket stays around for the entire sliding window.

## 3. Policies

```go
// cloud/internal/ratelimit/policies.go
type Policy struct {
    Scope      string
    Window     time.Duration
    Limit      int
    ActorFunc  func(r *http.Request) string
}

var Policies = []Policy{
    {Scope: "auth_register",  Window: time.Hour,    Limit: 10,  ActorFunc: ipBlock},
    {Scope: "auth_login",     Window: 15*time.Minute, Limit: 10, ActorFunc: ipEmail},
    {Scope: "auth_forgot",    Window: time.Hour,    Limit: 5,   ActorFunc: ipEmail},
    {Scope: "auth_resend",    Window: time.Hour,    Limit: 5,   ActorFunc: userID},
    {Scope: "claim_init",     Window: time.Minute,  Limit: 10,  ActorFunc: ipBlock},
    {Scope: "claim_redeem",   Window: time.Minute,  Limit: 10,  ActorFunc: ipBlock},
    {Scope: "billing_checkout", Window: time.Hour,  Limit: 30,  ActorFunc: userID},
    {Scope: "relay_per_subdomain", Window: time.Minute, Limit: 600, ActorFunc: ipSubdomain},
    {Scope: "relay_free_user", Window: 24*time.Hour, Limit: 100, ActorFunc: userIDFreeOnly},
    {Scope: "push_devices",   Window: time.Hour,    Limit: 30,  ActorFunc: userID},
    {Scope: "push_dispatch",  Window: time.Hour,    Limit: 1000, ActorFunc: serverID},
    {Scope: "admin",          Window: time.Minute,  Limit: 600, ActorFunc: adminUserID},
}
```

### 3.1 Actor functions

- `ipBlock`: IPv4 `/24` or IPv6 `/64` (truncate via `net.IPNet`).
- `ipEmail`: concat(ipBlock, sha256(email)).
- `userID`: JWT sub.
- `serverID`: from `X-Server-Token` bearer hash → cached.
- `ipSubdomain`: `(ipBlock, host_subdomain)`.

When the request's Cloudflare IP allow-list passes (ASN check via cached list), trust `Cf-Connecting-Ip`. Otherwise use `r.RemoteAddr`.

## 4. Middleware

```go
// cloud/internal/ratelimit/middleware.go
func Limit(pol Policy, c *Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            actor := pol.ActorFunc(r)
            if actor == "" { next.ServeHTTP(w, r); return }   // no actor → skip
            limit, window := c.EffectiveLimit(r.Context(), pol, actor)
            sum, allowed, err := c.checkAndIncrement(r.Context(), pol.Scope, actor, limit, window)
            if err != nil {
                if errors.Is(err, ErrRedisDown) {
                    w.Header().Set("Retry-After", "30")
                    problem(w, 503, "dependency_down", ""); return
                }
                problem(w, 500, "internal", ""); return
            }
            w.Header().Set("X-RateLimit-Limit",     strconv.Itoa(limit))
            w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max(0, limit-sum)))
            w.Header().Set("X-RateLimit-Reset",     strconv.FormatInt(time.Now().Add(window).Unix(), 10))
            if !allowed {
                w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
                writeJSON(w, 429, map[string]any{
                    "error":"rate_limit","retry_after":int(window.Seconds()),
                    "limit": limit, "window": window.String(),
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

`EffectiveLimit`: reads `rate_overrides` (LRU 60s). Lower-of (policy default, override).

## 5. Per-route wiring

```go
r.Route("/api/auth", func(r chi.Router) {
    r.Use(Limit(authRegisterPolicy, rl))
    r.Post("/register", register)
    // ...
})
r.Route("/api/push", func(r chi.Router) {
    r.With(Limit(pushDispatchPolicy, rl)).Post("/dispatch", dispatch)
    r.With(Limit(pushDevicesPolicy, rl)).Post("/devices", registerDevice)
})
```

Relay path (25.9) uses `Limit(relayPerSubdomainPolicy, rl)` middleware.

## 6. Free-tier daily quota

`relay_free_user` policy uses a 24h window with 100 requests. Resets at midnight UTC; we use a separate key shape: `rl:relay_free:{user}:{YYYY-MM-DD}` and skip the sliding-window math (a flat counter is enough for "daily resets at midnight").

## 7. Cloudflare IP allow-list

`cloud/internal/security/cf_iplist.go` fetches https://www.cloudflare.com/ips-v4 + ips-v6 daily into memory. Helper `IsCloudflareEdge(ip net.IP)` consulted by `ipBlock` to decide whether to honor `Cf-Connecting-Ip`.

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestLuaIncrementAtomic` | 1000 concurrent → exact count. |
| `TestIPv4_24Bucketing` | Two IPs in same /24 share counter. |
| `TestIPv6_64Bucketing` | Same. |
| `TestSlidingWindowSmoothBoundary` | No "jump back" at minute boundary. |
| `TestScopeKeyCollision` | `user_X` vs `server_X` distinct via scope prefix. |

### 8.2 Integration

| Test | Pins |
|---|---|
| `TestRedisDownFailClosed` | Redis killed; endpoint returns 503 + retry-after. |
| `TestOverrideApplied` | Insert override row; limit honored. |
| `TestRelayPerSubdomainCap` | 601st request in 60s → 429. |
| `TestFreeUserDailyQuota` | 101st → 429 with retry-after to midnight UTC. |
| `TestPushServerCap1000Hour` | 1001st → 429 + abuse. |
| `TestAdmin600PerMin` | 601st → 429. |
| `TestStripeWebhookBypass` | Signature path skips middleware. |
| `TestHeadersSetEvenOnAllow` | Limit/Remaining/Reset headers. |
| `TestCloudflareIPHonorsHeader` | When source IP in CF range, header trusted. |
| `TestNonCFRejectsHeader` | When not, header ignored. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| CGNAT /24 sharing | Limits sized for legitimate carrier traffic. | Spec. |
| Behind CF | `Cf-Connecting-Ip` trusted only from CF IPs. | `TestCloudflareIPHonorsHeader`. |
| Bots learn limits | Documented in API docs; abuse signals beyond limits. | Doc. |
| Stripe webhook | Signature; no rate limit. | `TestStripeWebhookBypass`. |
| Sliding-window boundary variance | Up to ±20% per spec. | `TestSlidingWindowSmoothBoundary`. |
| Refund of client-errors | Not refunded; documented. | Spec. |
| Header/body symmetry | Same numbers in both. | `TestHeadersSetEvenOnAllow`. |
| Redis Sentinel failover | Lua replicated; ≤ 5s loss. | Ops. |
| Time-of-day bursts (free 100/day) | Flat quota, midnight UTC reset. | Implementation. |
| Per-region rate limit | Out for v1. | Spec. |

## 10. Dependencies

- 25.1.
- 25.5 (user identity).
- 25.9 (relay middleware wiring).
- 25.17 (push limits).
- 25.20 (admin route limits).

## 11. Acceptance checklist

- [ ] Migration 00090002 (rate_overrides) applies.
- [ ] Redis Lua atomic INCR; sliding window.
- [ ] Fail-closed on Redis down.
- [ ] Headers always set.
- [ ] Overrides table consulted via LRU.
- [ ] All policy rows wired to routes.
- [ ] Cloudflare IP allow-list refreshed daily.
- [ ] Tests in §8 pass.
