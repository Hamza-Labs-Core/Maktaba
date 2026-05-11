# Implementation Plan — Story 25.12 Tier enforcement

> Companion to [story-25-12-tier-enforcement.md](story-25-12-tier-enforcement.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Source of truth | `cloud_subscriptions` (Stripe webhook 25.14 writes here). Free is the implicit fallback if no row. |
| Hot cache | Per-pod LRU `user_id → TierState` with 60s TTL + capacity 50,000 entries. Invalidated by Postgres `LISTEN tier_changed`. |
| Gate location | `TierGate.Acquire(ctx, userID, isStream) (release, error)` called inside relay proxy (25.9) before tunneling. |
| Concurrent streams | `HLEN streams:{server_id}` (25.11) with caveat — counts per-server. Cross-server enforcement uses a second Redis key `streams:user:{user_id}`. |
| Bandwidth caps | 105% soft (email warning), 110% hard (402), 200% circuit-breaker (forcibly close streams). |
| Out of scope | Tier *changes* (25.13/25.14). Stripe stuff (25.13). Customer portal (25.13). |

## 1. Tier table

```go
// cloud/internal/billing/tier.go
type Plan string
const (
    PlanFree         Plan = "free"
    PlanProMonthly   Plan = "pro_monthly"
    PlanProYearly    Plan = "pro_yearly"
    PlanFamilyMonthly Plan = "family_monthly"
    PlanFamilyYearly  Plan = "family_yearly"
    PlanSuspended    Plan = "suspended"
)

type TierState struct {
    Plan                Plan
    ConcurrentStreams   int            // 0 / 2 / 5
    MonthlyGBCap        int            // 0 / 100 / 500
    ServersCap          int            // 1 / 2 / 5
    DailyAPIQuotaBytes  int64          // free=100MB, paid=∞
    CurrentPeriodEnd    time.Time
    Status              string         // "active" | "past_due" | "canceled"
    ExpiresAt           time.Time      // cache field; not the same as period_end
}

var tierMatrix = map[Plan]TierState{
    PlanFree:          {Plan: PlanFree,         ConcurrentStreams: 0,  MonthlyGBCap: 0,   ServersCap: 1, DailyAPIQuotaBytes: 100 * 1<<20},
    PlanProMonthly:    {Plan: PlanProMonthly,    ConcurrentStreams: 2,  MonthlyGBCap: 100, ServersCap: 2, DailyAPIQuotaBytes: 0},
    PlanProYearly:     {Plan: PlanProYearly,     ConcurrentStreams: 2,  MonthlyGBCap: 100, ServersCap: 2, DailyAPIQuotaBytes: 0},
    PlanFamilyMonthly: {Plan: PlanFamilyMonthly, ConcurrentStreams: 5,  MonthlyGBCap: 500, ServersCap: 5, DailyAPIQuotaBytes: 0},
    PlanFamilyYearly:  {Plan: PlanFamilyYearly,  ConcurrentStreams: 5,  MonthlyGBCap: 500, ServersCap: 5, DailyAPIQuotaBytes: 0},
    PlanSuspended:     {Plan: PlanSuspended,    ConcurrentStreams: 0,  MonthlyGBCap: 0,   ServersCap: 0, DailyAPIQuotaBytes: 0},
}
```

## 2. Resolver + LRU cache

```go
type TierResolver struct {
    db     *pgxpool.Pool
    cache  *lru.Cache[uuid.UUID, *cachedTier]
    listen *pgsub.Subscription   // LISTEN tier_changed
    clock  clock.Clock
    meter  UsageReader           // 25.11 reads
}

type cachedTier struct {
    state    TierState
    fetched  time.Time
}

func (r *TierResolver) Get(ctx context.Context, userID uuid.UUID) (TierState, error) {
    if c, ok := r.cache.Get(userID); ok && time.Since(c.fetched) < 60*time.Second {
        return c.state, nil
    }
    plan, periodEnd, status, suspended, err := r.db.ReadSubscription(ctx, userID)
    if err != nil { return TierState{}, err }
    base := tierMatrix[plan]
    if suspended { base = tierMatrix[PlanSuspended] }
    base.CurrentPeriodEnd = periodEnd
    base.Status = status
    r.cache.Add(userID, &cachedTier{state: base, fetched: time.Now()})
    return base, nil
}

// LISTEN handler:
func (r *TierResolver) onTierChanged(payload string) {
    uid, err := uuid.Parse(payload); if err != nil { return }
    r.cache.Remove(uid)
}
```

Family-plan membership: in v1 the *payer* is the user_id on the subscription; member accounts are looked up via `cloud_family_members(payer_id, member_user_id)` (lives in 25.13 migration). Resolver reads payer's `Plan` for any member: pseudocode `if member, lookup payer; substitute payer's plan`.

## 3. Tier gate

```go
type TierGate struct {
    resolver *TierResolver
    redis    *redis.Client
    meter    UsageReader        // reads bandwidth_daily this month
    notifier QuotaNotifier      // email + push (25.17)
    clock    clock.Clock
}

func (g *TierGate) Acquire(ctx context.Context, userID, serverID uuid.UUID, isStream, isAPI bool, reqBytes int) (Release, error) {
    state, err := g.resolver.Get(ctx, userID)
    if err != nil { return noopRelease, fmt.Errorf("tier_lookup: %w", err) }
    if state.Plan == PlanSuspended { return noopRelease, errSuspended }

    // Free-tier API daily quota
    if isAPI && state.Plan == PlanFree {
        used, _ := g.redis.IncrBy(ctx, fmt.Sprintf("api_quota:%s:%s", userID, today()), int64(reqBytes)).Result()
        if used > state.DailyAPIQuotaBytes {
            return noopRelease, errDailyQuotaExceeded
        }
        g.redis.Expire(ctx, fmt.Sprintf("api_quota:%s:%s", userID, today()), 36*time.Hour)
    }

    // Stream-specific gate
    if isStream {
        if state.ConcurrentStreams == 0 {
            return noopRelease, errQuotaExceeded  // 402 free tier
        }
        // Per-user count across all their servers.
        cnt, _ := g.redis.HLen(ctx, "streams:user:"+userID.String()).Result()
        if int(cnt) >= state.ConcurrentStreams {
            return noopRelease, errTooManyStreams
        }
        // Bandwidth-cap check.
        gbUsed := g.meter.MonthToDateGB(ctx, userID, g.clock.Now())
        switch {
        case gbUsed >= float64(state.MonthlyGBCap)*2:   // 200%
            return noopRelease, errCircuitBreaker
        case gbUsed >= float64(state.MonthlyGBCap)*1.10: // 110%
            return noopRelease, errQuotaExceeded
        case gbUsed >= float64(state.MonthlyGBCap)*1.05:
            g.notifier.WarnSoftCap(ctx, userID, gbUsed, state.MonthlyGBCap)
        }
        // Register stream
        streamID := uuid.NewString()
        pipe := g.redis.TxPipeline()
        pipe.HSet(ctx, "streams:user:"+userID.String(), streamID, time.Now().UnixNano())
        pipe.Expire(ctx, "streams:user:"+userID.String(), 6*time.Hour)
        _, _ = pipe.Exec(ctx)
        return func() {
            g.redis.HDel(context.Background(), "streams:user:"+userID.String(), streamID)
        }, nil
    }
    return noopRelease, nil
}
```

Errors map to HTTP:

| error | HTTP | body |
|---|---|---|
| `errSuspended` | 503 | `server_suspended` (per 25.9) |
| `errQuotaExceeded` (free) | 402 | `{"used_gb": ..., "cap_gb": ..., "renewal_at": "..."}` |
| `errQuotaExceeded` (110%) | 402 | same |
| `errTooManyStreams` | 429 | `{"limit": 2, "current": 2}`; `Retry-After: 5` |
| `errCircuitBreaker` | 402 | + close any open stream (abuse event) |
| `errDailyQuotaExceeded` | 429 | `daily_quota_exceeded`; `Retry-After` to midnight UTC |

## 4. Circuit breaker

When `errCircuitBreaker` fires:

1. `cloud_abuse_events` row `kind=tier_circuit_breaker, severity=4`.
2. Notify user: email + push.
3. Forcibly close all `streams:user:{user_id}` by emitting `RST_STREAM`-equivalent on each tunnel (call into 25.8 `Tunnel.CancelStream(streamID)`).
4. Cache state updated to suspended-until-period-end.

## 5. Cross-server cap

Per-server cap (200 streams default, per 25.9 EC) protects against a single server being hammered; per-user cap is the tier. Both enforced independently.

## 6. Tier-change notify

Webhook 25.14 issues `pg_notify('tier_changed', user_id)` at COMMIT. The LRU caches across pods listen and evict. Worst case 60s staleness covers a missed notify.

## 7. Test plan

### 7.1 Unit

| Test | Pins |
|---|---|
| `TestTierMatrixIsExhaustive` | Every Plan constant has a row. |
| `TestProPlanAccepts2Streams` | Counter at 2 with new request → 429. |
| `TestFreeStreamRejected402` | Free user → 402. |
| `TestBandwidth105SoftWarn` | Quota 105% → warning email queued; stream accepted. |
| `TestBandwidth110Hard402` | 110% → 402. |
| `TestBandwidth200BreakerCloses` | 200% → all streams cancelled via tunnel call. |
| `TestSuspendedPlanRejects` | suspended → 503 across the board. |
| `TestFamilyMemberInheritsPayerPlan` | Member queries → Payer's plan. |
| `TestLISTENTriggersInvalidation` | LISTEN payload → cache evict. |
| `TestStaleCacheBound60s` | After 60s, cache miss → DB read. |

### 7.2 Integration

| Test | Pins |
|---|---|
| `TestWebhookDowngradeWithin5s` | Stripe webhook → notify → relay refuses next stream within 5s. |
| `TestConcurrentOpenBySecondDevice` | 2 streams via device A; device B opens 3rd → 429. |
| `TestStaleStreamReaperUnblock` | Phantom row reaped → next open succeeds. |
| `TestYearlyEquivCap` | Family yearly cap = 500 GB/mo. |
| `TestFreeDailyAPIQuota` | Free user 100MB/day; 101st request 429. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Pause/resume mid-stream | Counts as two temporally adjacent streams; concurrent cap unaffected at idle. | Spec. |
| Buffer pre-roll | If open fails, user sees player error; tier reason surfaced via response body. | Spec. |
| Trial / promo | Out of v1. | Spec. |
| Family invitees | Payer's plan inherited by members. | `TestFamilyMember`. |
| Server suspended | 503 across all that user's servers. | `TestSuspendedPlanRejects`. |
| Refund mid-cycle | Subscription stays active until period_end; honored. | Spec. |
| Webhook delivery delay | Cloud state is the truth, not Stripe; reconciliation cron (25.14). | Cross-story. |
| Pricing change | Applies at next renewal; current row is contract. | Spec. |
| Free-tier 100MB/day API | Excluded from media; enforced here. | `TestFreeDailyAPIQuota`. |
| Soft-cap email spam | Send at most one per 24h per user via `notifier.WarnSoftCap` dedup. | Notifier dedup key. |

## 9. Dependencies

- 25.1 (foundation, pgsub).
- 25.6 (`cloud_servers` for server cap check, if surfaced).
- 25.9 (proxy is the call site).
- 25.11 (Redis stream registration + monthly-to-date counter).
- 25.13/25.14 (Stripe rows; notify channel).
- 25.17 (push notify of soft-cap warnings).

## 10. Acceptance checklist

- [ ] Tier matrix matches story table.
- [ ] LRU cache 60s TTL with LISTEN invalidation.
- [ ] Stream open gates against tier `ConcurrentStreams`.
- [ ] 105/110/200% bandwidth tiers wired with notifier + circuit breaker.
- [ ] Free daily 100 MB API quota enforced.
- [ ] Family member inherits payer's plan.
- [ ] All tests in §7 pass.
