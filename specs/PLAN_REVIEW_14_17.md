# Implementation Plan Review — Epics 14-17

**Scope.** All 33 implementation plans paired with their stories across four
epics on `claude/affectionate-bardeen-7fa3fd`:

| Epic | Title | Plans |
|------|-------|-------|
| 14 | TV Apps (tvOS / Android TV) | 7 |
| 15 | Discovery & Networking | 7 |
| 16 | Subscriptions & Monetization | 8 |
| 17 | UX Design System | 11 |

**Method.** Each epic reviewed against `specs/architecture.md` (canonical
schema §8, API surface §9, gRPC §9.9, auth §9.8, client topology §6.5) and
against the matching story acceptance criteria. Cross-referenced against the
plans landed in earlier epics (07-13) and the prior review at
[`specs/PLAN_REVIEW_07_13.md`](PLAN_REVIEW_07_13.md).

**Verdict at a glance.**

| Epic | Overall | Blocking | Major | Minor |
|------|---------|----------|-------|-------|
| 14 | drift-heavy | 4 | 5 | 6 |
| 15 | drift-heavy | 5 | 7 | 5 |
| 16 | mostly-clean-but-mismodeled | 3 | 6 | 8 |
| 17 | strong | 1 | 6 | 9 |

Three plans across the four epics could ship as-is
([plan-17-03](specs/epics/17-ux-design-system/plan-17-03-motion.md),
[plan-17-04](specs/epics/17-ux-design-system/plan-17-04-loading-states.md),
[plan-17-05](specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md)).
The most severe issues are **two cases of double-ownership** (the
recommendations endpoint and the pairing endpoint) where two plans in
different epics define the same route with incompatible schemas; until one is
chosen the affected stories cannot be implemented coherently.

---

## 1. Top-priority cross-cutting issues

Issues below appear in multiple epics. Fixing them in one place avoids
fixing them N times in plan-by-plan edits.

### 1.1 `/api/recommendations` is double-owned — affects Epic 7 and Epic 14

[plan-07-21](specs/epics/07-api-server/plan-07-21-recommendations.md) and
[plan-14-07](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) **both**
own `GET /api/recommendations` with **incompatible schemas**:

| | plan-07-21 | plan-14-07 |
|---|------------|------------|
| Migration | `0021_user_recs.sql` | `0042_recommendation_runs.sql` |
| Storage | `user_recs(user_id, video_id, rail_kind, score, computed_at)` — one row per (user, video, rail) | `recommendation_runs(user_id PK, rows JSONB)` — one row per user |
| Cache | per-process in-memory, 60 s | DB-resident `expires_at` 24 h + singleflight inline |
| Route shape | `?surface={web-home\|tv-home\|mobile-home}&limit=N` | unparameterized; localized server-side |
| Rails / kinds | `continue, next-up, for-you, library` | `more_from_speaker, similar_to_video, newly_added, editor_picks, library_recap, speakers_you_follow` |
| Mutating endpoints | none | `DELETE /rows/{kind}`, `DELETE /items/{id}`, `POST /refresh` |
| Dismissal table | n/a | `recommendation_dismissals` |

These cannot coexist. Both stories specify the same endpoint. **Recommendation:**
adopt plan-14-07's schema (cache-row-per-user with JSONB rail array is the
better model for the dismissal-aware case 14.6 needs) and rewrite plan-07-21
to consume that data, OR mark 7.21 as the canonical implementation and
rewrite 14.6/14.7 to consume `?surface=tv-home`. Either choice unblocks both
stories; the present state blocks both.

### 1.2 `/api/auth/pair*` is double-owned — affects Epic 10 and Epic 15

[plan-10-17](specs/epics/10-auth-security/plan-10-17-auth-pair.md) and
[plan-15-06](specs/epics/15-discovery/plan-15-06-pairing-api.md) **both** own
`POST /api/auth/pair`, `POST /api/auth/pair/claim`, etc., with incompatible
designs:

| | plan-10-17 | plan-15-06 |
|---|------------|------------|
| Migration | `0027_pairing_codes.sql` | `0053_pairing_codes.sql` |
| Code storage | `code_hash` (argon2) — plaintext NEVER stored | `code TEXT PRIMARY KEY` — plaintext IS the PK |
| Lifecycle marker | `state TEXT IN ('pending','claimed','expired')` | `claimed_at TIMESTAMPTZ` (nullability) |
| Concurrent-claim error | 409 `pair-code-already-claimed` (per plan-10-17:527-529) | 400 `code-already-claimed` (per plan-15-06:295) |
| Nonce field | absent | `BYTEA NOT NULL CHECK (octet_length=32)` (claim path verifies nonce from QR) |
| `device_kind` field | absent | `CHECK (device_kind IN ('mobile','desktop','tv'))` |
| Polling endpoint | `GET /api/auth/pair/{code}` | not present — uses unauthenticated `GET /api/auth/pair` (the issuer's own list) |
| Sweeper | Python (Pipeline reaper) | Go goroutine in API service |

This is more than a schema fork — the two designs target **different security
models**. plan-10-17 hashes the code (defense against DB compromise);
plan-15-06 stores plaintext but requires a 32-byte nonce that's only on the
QR. Story 15.5 only works with plan-15-06's nonce design (TC: "the manual
path is treated as lower-trust" because it lacks nonce). Story 10.17 only
works with plan-10-17's `state` enum sweeper.

**Recommendation.** plan-15-06's nonce + plaintext PK is the right model for
the QR-flow security story (defeats the hashed-code-in-QR-but-leaked-DB
threat by binding QR to a separate secret), but plan-10-17's `state` enum is
cleaner than the `claimed_at IS NULL` shape. Merge: nonce + `code_hash` + state
enum. Then pick one migration number and delete the other plan's migration.
Update plan-10-17's `auth-pair` story EC to match the 400 vs 409 choice.

### 1.3 Ed25519 attributed to Epic 10 Story 10.6, which is RSA — affects Epics 15, 16

[plan-15-07](specs/epics/15-discovery/plan-15-07-federation-api.md):16,
[plan-16-04](specs/epics/16-subscriptions/plan-16-04-license-validation.md):11,
[plan-16-06](specs/epics/16-subscriptions/plan-16-06-feature-flags.md):17, and
[plan-16-08](specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md):14
all reference "Epic 10 Story 10.6 long-term Ed25519 keys" /
"Ed25519 key infrastructure".

[plan-10-06:89,115,134,357](specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md)
generates **RSA-4096** and signs **RS256** JWTs — there is no Ed25519 key
material in Epic 10. The Story 10.6 title is literally `RS256 keys, rotation,
JWKS`.

The downstream plans need either:
- (a) a new Epic 10 story owning Ed25519 long-term key generation (the
  plans assume it's bundled at build time, signed against, and rotated), or
- (b) a rewrite to use RS256 for the federation handshake / feature flags /
  license signing, accepting the larger signature size and slower
  verification.

**Recommendation.** Add Epic 10 Story 10.18 "Ed25519 long-term server identity
keys" or fold into Story 10.6 — either way, **the assumption that 10.6
already produces Ed25519 is the current blocker**, since federation, license
validation, and feature-flag signing all chain off it.

### 1.4 `audit_log.category` enum extended in 4 places without owning the migration — affects Epics 15, 16

[plan-09-17:148](specs/epics/09-library-management/plan-09-17-library-audit.md)
declares `CHECK (category IN ('library','security'))`. Multiple plans in
Epics 14-17 write categories outside that set:

| Plan | New `category` value | Where written |
|------|----------------------|----------------|
| [plan-15-06:15,273](specs/epics/15-discovery/plan-15-06-pairing-api.md) | `'pair'` | every pair create/claim/revoke |
| [plan-15-07:368](specs/epics/15-discovery/plan-15-07-federation-api.md) | `'federation'` | every federation init/pair/confirm/revoke |
| [plan-16-08:16](specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md) | `'flags'` | every admin flag override write |
| [plan-16-03:200](specs/epics/16-subscriptions/plan-16-03-subscription-management.md) | `'subscription'` (implicit; not stated but the disputed-payment branch calls `s.audit`) | dispute, plan change |

This is the same class of bug the previous review noted in §1.8 for Epic 12's
`'device'`. Every one of these inserts will fail the CHECK constraint.

**Recommendation.** Owner: [plan-09-17](specs/epics/09-library-management/plan-09-17-library-audit.md).
Either expand the CHECK enum to
`('library','security','pair','federation','flags','subscription','device')`,
or remove the CHECK and validate at the application layer. The plan also
needs to land an explicit `ALTER TABLE … DROP CONSTRAINT … ADD CONSTRAINT` or
a versioned migration adopted by the dependent plans.

### 1.5 `playback_state.duration_sec` referenced but never defined — affects Epic 14

Architecture §8.5 lines 1512-1519:

```sql
CREATE TABLE playback_state (
    user_id      UUID, video_id UUID,
    position_sec REAL NOT NULL,
    completed    BOOLEAN NOT NULL DEFAULT false,
    updated_at   TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);
```

There is no `duration_sec` on `playback_state`. Multiple plans assume it:

- [plan-14-05:32-36,52-53](specs/epics/14-tv-apps/plan-14-05-continue-watching.md)
  partial index uses `WHERE position_sec >= duration_sec * 0.05 AND
  position_sec < duration_sec * 0.95` — refers to a non-existent column. The
  Postgres planner will reject the index.
- [plan-14-05:69-87](specs/epics/14-tv-apps/plan-14-05-continue-watching.md)
  `GetContinueWatching` query selects `ps.duration_sec` — also broken.
- [plan-14-07:120-124](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md)
  `WatchedFeatures` query: `ps.position_sec >= ps.duration_sec * 0.05` — same.

Epic 7 Story 7.11 ([plan-07-11](specs/epics/07-api-server/plan-07-11-watch-progress-sync.md)
:75) only adds `source_session_id` to `playback_state`, not `duration_sec`.
Plan 07-21 reads `mi.duration_sec` from `media_info` (which itself doesn't
have `duration_sec` — the previous review §1.1 noted `videos.duration_sec` is
the canonical source, line 1318).

**Recommendation.** Plans 14-05 and 14-07 must JOIN through `videos` and
read `videos.duration_sec`. Update the partial index to either (a) drop the
percentage predicate (just `(user_id, updated_at DESC)`, materialize the
duration filter at query time), or (b) ALTER `playback_state` to add
`duration_sec REAL` so the partial-index optimization is realizable, and
own that migration in plan-07-11 since that's where playback writes happen.

### 1.6 `videos.poster_url` and `videos.deleted_at` referenced but not defined — affects Epics 14, 16

Architecture §8.1 line 1316: `poster_path` (not `poster_url`). No
`videos.deleted_at` column exists.

| Plan | Drift | Line |
|------|-------|------|
| [plan-14-05:76-77](specs/epics/14-tv-apps/plan-14-05-continue-watching.md) | `v.poster_url, v.deleted_at` | sqlc query |
| [plan-14-07:122](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) | `AND v.deleted_at IS NULL` | sqlc query |

Same drift identified in the prior review §1.1; left unresolved upstream and
inherited by these plans. Decide once: either (a) rename to `poster_path`
and drop deleted-checks (use a soft-delete view if needed) or (b) add
`videos.poster_url` (alias) and `videos.deleted_at` to architecture §8.1 and
own the ALTER in [plan-07-04](specs/epics/07-api-server/plan-07-04-video-crud.md)
or [plan-09-15](specs/epics/09-library-management/plan-09-15-library-deletion.md).

### 1.7 Migration number conflict 0040 — affects Epics 12 and 14

[plan-12-10:23-41](specs/epics/12-mobile/plan-12-10-device-registration-api.md)
ships `0040_devices.sql`.
[plan-14-05:13](specs/epics/14-tv-apps/plan-14-05-continue-watching.md)
ships `0040_playback_state_continue_idx.sql`.

Both migrations cannot apply with the same numeric prefix; goose orders by
filename. **Recommendation.** Renumber 14-05 to 0042 and let the rest of
Epic 14/16 cascade (this also frees 14-07's 0042 — push it to 0043).

### 1.8 New routes added outside architecture §9 — affects Epics 14, 15, 16, 17

The following routes are introduced by 14-17 plans but are not in
architecture §9:

| Route | Owner |
|-------|-------|
| `POST /api/auth/pair` family | plan-10-17 / plan-15-06 (also noted §1.2) |
| `GET /api/recommendations`, `DELETE /rows/{kind}`, `DELETE /items/{id}`, `POST /refresh` | plan-14-07 (also noted §1.1) |
| `GET /api/.well-known/apple-app-site-association` (implied by plan-12-09 dependency on plan-15-05) | unowned |
| `GET /api/system/cert-rotation` | plan-15-02 |
| `POST /api/federation/init`, `/pair`, `/{id}/confirm`, `/{id}/token`, `PATCH /{id}`, `DELETE /{id}` | plan-15-07 |
| `POST /api/billing/portal-session`, `POST /api/billing/webhook`, `POST /api/billing/cancel` | plan-16-03 |
| `POST /api/admin/license`, `DELETE /api/admin/license`, `GET /api/admin/license` | plan-16-04 |
| `POST /api/telemetry`, `POST /api/telemetry/web-vitals`, `DELETE /api/telemetry/devices/{p}` | plan-16-07 |
| `GET /api/me/flags`, `POST /api/me/cohorts`, `/api/admin/flags*` | plan-16-08 |
| `GET /api/admin/relay/usage` | plan-16-02 |
| `GET /api/setup/state`, `PATCH /api/setup/state`, `POST /api/setup/complete`, `POST /api/setup/tour/dismiss`, `GET /api/setup/stt-probe`, `POST /api/libraries/probe`, `POST /api/libraries/init-default` | plan-17-06 |

All of these are legitimate extensions but architecture §9 needs to enumerate
them so the API surface remains the single source of truth. Several
(`/api/me/flags`, `/api/me/cohorts`, `/api/me/preferences`) overlap the `/api/me/*`
namespace previously claimed by Web UI plans (the prior review §1.11 already
flagged this).

### 1.9 Pipeline stage `subtitle_gen` re-confirmed without architecture canonicalization — affects Epic 17

[plan-17-10:105](specs/epics/17-ux-design-system/plan-17-10-processing-progress.md)
declares the canonical 7-stage strip:
`scan, probe, extract, transcribe, subtitle_gen, index, thumbnail`. This
matches the previous review §1.6 — the plans assume the stage exists, but
architecture §3 still doesn't list it. Plan-17-10 even cites
[REVIEW §1.3.b/c](REVIEW.md) as authority — but architecture is unchanged.

**Recommendation.** Update architecture §3.5 to declare `subtitle_gen` as
its own pipeline stage (the simpler resolution; plan-17-10 expects it).

### 1.10 Reach UI is deprecated — affects Epic 17

[plan-17-02:91-96,233](specs/epics/17-ux-design-system/plan-17-02-component-library.md)
imports `@reach/dialog` for the `Modal` primitive and lists it under
Dependencies. **Reach UI was deprecated in 2022** and has been unmaintained
since; the maintainer (Ryan Florence) recommends Radix UI as the successor.
Plan 17-02 already imports `@radix-ui/react-tooltip`, `@radix-ui/react-tabs`,
`@radix-ui/react-context-menu` — `@radix-ui/react-dialog` is the correct
choice for this `Modal` component.

### 1.11 `tokens.json` aliases reference an undefined `color.neutral` group — affects Epic 17

[plan-17-01:25-30](specs/epics/17-ux-design-system/plan-17-01-design-tokens.md)
defines `color.semantic.bg = {color.neutral.50}`, `color.semantic.fg =
{color.neutral.900}`, but the `color` block declares only `brand.50/500/900`
plus the `semantic` aliases — no `color.neutral.*` exists. Style Dictionary
will fail with "alias unresolved".

Same plan §7 lists "Token graph cycle" as an EC pinned by `TestCycleDetected`,
but the more pressing case is "missing alias", which is what this is. Add
`color.neutral.50`, `color.neutral.100`, ..., `color.neutral.900` to the
base `tokens.json`.

### 1.12 `media_features` table and pgvector dependency unowned — affects Epic 14

[plan-14-07:127](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md)
relies on `media_features.embedding <=> seed_embedding` (pgvector cosine
operator). The previous review §10.2 #10 noted that `media_features` is
mentioned in plan-09-10 but no migration ships the table. Plan-14-07 just
re-references the gap, without owning it.

`pgvector` is also not in architecture §2.1's Postgres extensions list (only
`pg_trgm` is mentioned, plus FTS via `tsvector` natively). Adding pgvector
needs an explicit architecture decision.

**Recommendation.** Either own `media_features` + pgvector in plan-09-10 (the
expected location) or punt to plan-14-07 (currently the only consumer).
Architecture §2.1 needs the pgvector entry.

### 1.13 Unauthenticated public DELETE endpoint — affects Epic 16

[plan-16-07:217-226](specs/epics/16-subscriptions/plan-16-07-telemetry-api.md)
`DELETE /api/telemetry/devices/{pseudonym}` is mounted **outside** any
`requireAuth` middleware (the surrounding `r.Use(rateLimitPerIP(...))` is the
only gate). The plan rationalizes this as "pseudonym is a self-asserted
bearer", with rate limiting as the only defense.

This is not crazy (the pseudonym is 96 random bits → infeasible to
brute-force), but it is a deliberate deviation from the rest of the
auth model and worth documenting at the architecture level. Story 16.5
"Forget my device" expects the user to delete *their* device, but the
endpoint as designed lets anyone with the pseudonym delete it.

**Recommendation.** Either (a) require auth and check the pseudonym is
attached to the caller's user (requires a `pseudonym → user_id` map, which
the plan deliberately avoids for privacy), or (b) accept the trade-off and
document explicitly in the privacy doc. The `Forget my device` button in the
UI knows the pseudonym anyway, so the public-DELETE design works in practice.

### 1.14 `videos.codec` referenced but column lives on `media_info` — affects Epic 15

[plan-15-04:140-141](specs/epics/15-discovery/plan-15-04-dlna-upnp.md): "Filter
applied in every `Browse` query: `WHERE codec IN ('h264','aac','mp3',
'jpeg','mpeg4')`". This implies `videos.codec`, but architecture §8.1 puts
codec on `media_info.video_codec` (line 1329).

Same plan §6 references `library.root_path` and `library.path` for path
allowlist checks — architecture has `libraries.roots TEXT[]` (plural). Same
drift the previous review §1.1 noted with `library_roots`.

### 1.15 Singleton-row migration pattern proliferates — affects Epics 15, 16, 17

Singleton config rows (`id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1)`)
appear in 5 new tables introduced across these epics:

- `server_identity` ([plan-15-01](specs/epics/15-discovery/plan-15-01-mdns.md))
- `relay_settings` ([plan-15-02](specs/epics/15-discovery/plan-15-02-cloud-relay.md))
- `dlna_settings` ([plan-15-04](specs/epics/15-discovery/plan-15-04-dlna-upnp.md))
- `licenses` ([plan-16-04](specs/epics/16-subscriptions/plan-16-04-license-validation.md))
- `onboarding_state` ([plan-17-06](specs/epics/17-ux-design-system/plan-17-06-onboarding.md))

This is a coherent pattern but it isn't documented as a convention; multi-user
deployments (Epic 16 `home`/`pro` tiers allow ≥4 seats) will need at least
`onboarding_state` to be per-user, and likely `licenses` to be aware of
which user paid (already partly done via `billing_customers.user_id`). Worth a
cross-cutting note in architecture so future plans don't need to re-decide.

---

## 2. Epic 14 — TV Apps (7 plans)

**Migration numbers:** `0040`, `0042`. The `0040` collides with Epic 12 (§1.7).
**No internal route conflicts** within the epic; the cross-epic
`/api/recommendations` collision is in §1.1.

The recurring drift items in §1.1, §1.5, §1.6, §1.7, §1.8, §1.12 dominate.
Plan-specific findings beyond those:

### 2.1 [plan-14-01](specs/epics/14-tv-apps/plan-14-01-tvos.md) — endpoint name drift

- Line 189: `// POST /api/playback/state`. Architecture §9.4 line 1629 has
  `POST /api/stream/sessions/{id}/progress`. Same endpoint-name drift the
  previous review noted in Epic 11 (§1.11). Pick `/api/me/playback-state`
  (plan-11-02 / plan-11-10's chosen name) or
  `/api/stream/sessions/{id}/progress` (architecture's name) for *all*
  clients.
- Line 17: cross-references `[Story 15.6](../15-discovery/story-15-06-pairing-api.md)`
  but the call sites at lines 224-225 (`PairingAPI.create(deviceKind: .tv)`)
  imply plan-15-06's `device_kind` field — which doesn't exist on plan-10-17's
  schema. See §1.2.
- Line 17: "the TV is the *issuer* (calls `POST /api/auth/pair`)" — neither
  plan-10-17 nor plan-15-06 spells out who is the issuer vs. claimer. The
  TV-as-issuer model is implicit but should be documented in plan-10-17 /
  plan-15-06.

### 2.2 [plan-14-02](specs/epics/14-tv-apps/plan-14-02-android-tv.md) — Apollo plugin id mismatch

- Line 91: `id("com.apollographql.apollo3") version "4.0.0"`. Apollo Kotlin 4.x
  uses the plugin id `com.apollographql.apollo` (no `3` suffix); the `apollo3`
  id is the 3.x line. Build will fail with `Plugin not found`.
  See [Apollo Kotlin 4 migration notes](https://www.apollographql.com/docs/kotlin/migration/v4).
- Line 192: `GET /api/playback/state?in_progress=true` — same endpoint
  name drift as 14-01 §2.1. Architecture / Epic 7 own no such route.
- Line 246: bundle-size budget asserts `≤ 25 MB`. Story 14.2 has no explicit
  bundle target; if there's a project-wide budget, it should be in the epic
  README, not buried in an acceptance checklist.
- Line 16: "android.tvprovider:tvprovider — a `PreviewChannel` with
  `WatchNextPrograms`" — `WatchNextProgram` is the correct class
  (singular); "WatchNextPrograms" is a content-URI helper. Minor naming.

### 2.3 [plan-14-03](specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md) — generally clean

The 10-foot UI plan is the cleanest in Epic 14. One small issue: §1's
TV-specific token group `tv.*` extends `tokens.json` but plan-17-01 (the owner
of tokens) doesn't enumerate the `tv` group in its build platforms (§2). When
17-01 lands, it needs to add a `tv` platform output (or document that `tv.*`
is a sub-group of the existing `swift/tvos` and `kotlin/androidtv` outputs).

### 2.4 [plan-14-04](specs/epics/14-tv-apps/plan-14-04-voice-search.md) — generally clean

One Swift correctness issue at lines 62-63: `let auth = await
SFSpeechRecognizer.requestAuthorization()` — `requestAuthorization` is a
class method that takes a completion handler in iOS 13+. The `await`-only
form requires an explicit `withCheckedContinuation` wrapper, or the use of
the actor-isolated `SFSpeechRecognizer.authorizationStatus()` + an
async-bridged authorization step. As written this won't compile.

### 2.5 [plan-14-05](specs/epics/14-tv-apps/plan-14-05-continue-watching.md) — multiple blockers

Beyond §1.5/1.6/1.7 issues:

- Lines 32-36: `INCLUDE (video_id, position_sec, duration_sec)` — `video_id`
  is already in the PK; including it again in INCLUDE wastes index size.
- Line 78: `ORDER BY ps.updated_at DESC LIMIT $2` — but the query joins
  `videos` and adds `WHERE v.deleted_at IS NULL`; the `ORDER BY` happens after
  the join, so the partial index can't avoid a sort unless the planner picks
  a merge join. With 100k rows the test `TestIndexCovering` will likely fail
  to assert "Index Only Scan" because the join requires a Heap Fetch on
  `videos`. The "covering" claim should be qualified.
- Plan §3 says "Same video in two collections: One row in `playback_state`
  PK; one card only" — `playback_state` PK is `(user_id, video_id)` per
  architecture, so this invariant holds for free. The claim is correct, just
  redundantly tested.

### 2.6 [plan-14-06](specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md) — generally clean

- Line 79: `companion object { fun fromRaw(s: String) = values().firstOrNull
  { it.raw == s } }` — `Enum.values()` is replaced by `Enum.entries` in
  Kotlin 2.0; the deprecation will fire on the Apollo Kotlin 4 / K2 toolchain
  the rest of the epic targets.
- Line 31-35: localized titles via the server are great, but the response
  shape (`title`, `reason_kind`, `reason_args`) is the *plan-14-07* shape, not
  *plan-07-21*'s `Rail{ID, Title}` — see §1.1. Once §1.1 is resolved this
  becomes consistent.

### 2.7 [plan-14-07](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) — multiple blockers

Beyond §1.1 (double-ownership) and §1.5/1.6/1.12:

- Lines 188-194: `s.sf.Do(u.ID.String(), func() (any, error) { ... })` —
  Go's `singleflight.Group.Do` returns `(any, error, bool)`. The plan reads
  `rowOut, _, _ := s.sf.Do(...)` which is correct, but then uses
  `rowOut.([]RowOut)` — an untyped assert that panics if compose returned
  an error. The error from singleflight is dropped. Add explicit error
  handling.
- Lines 233-238: nightly per-user budget set to **200 ms** via
  `context.WithTimeout(ctx, 200*time.Millisecond)`. Story 14.7 AC says
  "≤ 200 ms wall on a 100k-segment library" but the wall budget is **per
  compose**, not the context deadline; setting a 200 ms ctx for a Go batch
  worker means 50% of users will get truncated/empty rows (compose probably
  averages 100-150 ms with realistic variance). Increase to 500 ms or budget
  it as p95 not deadline.
- Lines 102-110: `ListDismissals` returns `(kind, key)` rows but the dismissal
  filter at line 162 is `buildDismissalSet(dismissals, u.ID)` — `u.ID` is
  unused inside `buildDismissalSet` because the rows are already scoped by
  `WHERE user_id = $1`. Minor dead parameter.
- Localization: `i18n.Bundle(locale)` (line 254) — Epic 7/architecture has
  no i18n package. Either reuse Epic 7's localization (if it exists) or
  declare a new `api/internal/i18n` ownership (currently nowhere).

---

## 3. Epic 15 — Discovery & Networking (7 plans)

The cross-cutting items §1.2 (pairing double-own), §1.3 (Ed25519
mis-attribution), §1.4 (audit categories), §1.8 (new routes) cover the
biggest items. Plan-specific:

### 3.1 [plan-15-01](specs/epics/15-discovery/plan-15-01-mdns.md) — clean modulo cross-platform notes

- Line 78: `import "github.com/grandcat/zeroconf"` — the package is largely
  unmaintained (last release 2018; some forks). The plan's footnote
  ("or `github.com/hashicorp/mdns`") is honest but the choice should be made
  before implementation. `hashicorp/mdns` is the commonly-recommended
  alternative.
- Line 95: `bindInterfaces` references `cfg.BindIface` (singular string) but
  §0 says "comma-separated names → only those" — i.e., multiple. Either the
  config field is plural or `bindInterfaces` parses the comma split. Spec
  is ambiguous.
- §6: dedupe-by-`mdns_id` is a smart resolution to the hostname-change EC.
  The implementation at [`mergeServers.ts`](specs/epics/15-discovery/plan-15-01-mdns.md:189)
  is JS but is needed in 4 platforms (web/iOS/Android/desktop). Either share
  via a Capacitor plugin or duplicate per platform — pick one.

### 3.2 [plan-15-02](specs/epics/15-discovery/plan-15-02-cloud-relay.md) — multiple structural issues

- `services/relay/` is a brand-new service (relay edge) with its own
  protobuf, its own Redis-style counter, and its own deployment
  consideration. It is **not** in architecture §1.4 ("Service topology") —
  the architecture topology mentions only API, Streaming, Pipeline. The
  plan acknowledges "we route by `mdns_id`" but the architecture decision
  to add a fourth service (or to push relay edge to a third-party service
  like Cloudflare Tunnel — see Epic 16 README open question 2) is not
  made.
- Lines 200-219: SPKI pinning code shape is correct but the pin
  store-management pieces (key rotation flow, "Server identity changed" UX)
  reference no existing key-management code. The
  `URLSessionDelegate`/`OkHttp.CertificatePinner` choice is sound; the pin
  *update path* on `cert-rotation` is only sketched at line 238 ("client adds
  it to the pin set; after `next_until`, the old hash is removed").
- Line 195: `jws.Sign(body, cur.PrivateKey)` — the `body` is a Go map, not
  bytes; this needs canonicalization (JCS / RFC 8785) or the signature won't
  verify across server-platform serializers. plan-16-04 already adopted JCS;
  plan-15-02 should align.
- Line 17: "web (browser-side) cannot pin — documented limitation" is
  correct but the consequence is that **relay-routed web traffic has no
  end-to-end protection beyond browser CA trust**. Worth surfacing more
  prominently than a §6.3 footnote — it's a tier capability decision (the
  story implies pinning protects the *relay path*, but for web it does not).

### 3.3 [plan-15-03](specs/epics/15-discovery/plan-15-03-federation.md) — generally clean

- §6: "If the global content-hash uniqueness scope (per
  [REVIEW §1.1.a](REVIEW.md)) ends up settled as global, then the collision
  is impossible…". Architecture §8.1 line 1307 says `content_hash TEXT NOT
  NULL UNIQUE` — singleton uniqueness, not scoped. So the collision *is*
  impossible at the schema level today, and the conflict-resolution code
  (lines 152-156) is dead code. Either drop it, or change architecture to
  scope `UNIQUE` per `library_id` to permit federation collisions.
- Lines 130-141: `FederatedVideoStreamingURL` mints A's token via B
  (`r.federation.MintStreamingTokenOn(ctx, fo.PartnerID, v.ID)`). This is
  a server-to-server outbound from B to A. The auth model: A trusts B's
  long-term Ed25519 (per pin) — but the call is JWT, not signed with
  Ed25519. Where does B authenticate the call to A? Plan-15-07 §4.4
  describes federation JWT minting but is the API JWT *for federation
  endpoints* RS256 or Ed25519? The choice is buried; the plan needs to
  pick.

### 3.4 [plan-15-04](specs/epics/15-discovery/plan-15-04-dlna-upnp.md) — schema drift

Beyond §1.14:

- §4 implements `Browse` switching on `ObjectID`: `0/1/<genre>` → genres,
  `0/4/<...>` → recently added. Sections "Library", "Genre", "Speaker",
  "Recently Added" are listed in §0 but the `0/2`, `0/3` (Library, Speaker)
  branches aren't implemented in the switch. Stub or remove from list.
- Line 11: vendoring "the parts we need from `github.com/anacrolix/dms`" is
  hand-wavy. Pinning a fork commit and listing the files is a more
  legible plan; "vendor" without scope reads like a TODO.
- §3 SSDP advertiser: `validateAddr` (lines 87-92) refuses non-private
  addresses *at advertise time*. But the multicast group `239.255.255.250` is
  scoped to the local subnet anyway — the additional check is correct
  defense in depth, just slightly redundant. Worth a comment so it isn't
  removed as dead code.

### 3.5 [plan-15-05](specs/epics/15-discovery/plan-15-05-qr-pairing.md) — generally clean

- §3.3: manual entry path "treats nonce-less claims as lower trust";
  plan-15-06's claim handler at line 213-214 verifies nonce via constant-time
  compare — there's no relaxed-nonce code path. The plan reconciles by
  saying "manual entry shows a clear 'Use the QR code' message rather than
  fudging trust." OK as a v1 limitation, but the AC table at §10 still
  promises a manual path; either remove the manual claim AC or add a
  side-channel for nonce.
- Lines 191-200: TV view auto-refreshes 30 s before expiry; but plan-15-06
  ships a 5-min TTL hard-coded as `expires_at = created_at + 5min` (not
  shown but implied by §11). Refreshing 30 s before sounds fine; the
  in-flight scan claim path at line 199 says "previous code is valid for
  the full 5 minutes" — but if the TV has *already* posted a fresh code
  to the API, the previous code is still in `pairing_codes` (no DELETE) so
  this is correct. Worth a sentence in plan-15-06 §6 to confirm.

### 3.6 [plan-15-06](specs/epics/15-discovery/plan-15-06-pairing-api.md) — beyond §1.2

- Line 211: `if !subtle.ConstantTimeCompare(row.Nonce, in.Nonce) == 1` —
  precedence bug. `!subtle.ConstantTimeCompare(...) == 1` parses as
  `(!ConstantTimeCompare(...)) == 1` which always evaluates to `false`
  (booleans aren't 1). Should be `if subtle.ConstantTimeCompare(...) != 1`.
  This is a copy-paste error that defeats the whole nonce check.
- §4 `ClaimPairingCode :one` SQL returns the row, but Go pattern is
  `RETURNING *` for sqlc; the plan emits `RETURNING *` but doesn't pin
  whether sqlc's "exec returning one row" returns nothing on no-row-matched
  (as a `sql.ErrNoRows`) or a zero-value. The plan's race handling at line
  230 "race lost → ErrAlreadyClaimed" depends on this; trace the sqlc-generated
  Go code to confirm before merge.

### 3.7 [plan-15-07](specs/epics/15-discovery/plan-15-07-federation-api.md) — multiple deep issues

Beyond §1.3:

- §3.4 outright admits the spec's HMAC-vs-CRC token shape is "internally
  inconsistent" and unilaterally adopts CRC. This is a deliberate
  story↔plan deviation; updating story 15.7 to match the implementation
  reality is the cleaner fix. The plan flagging its own inconsistency is
  good; not resolving it before merge is the issue.
- Lines 103-110: **`acl JSONB`** is opaque; plan-15-03 §3 expects `acl =
  {libraries: [...], read_only: true}`. The shape isn't pinned anywhere
  with a CHECK or a sqlc type. Add a documented schema or a Go struct that
  unmarshals it.
- Lines 269-278: `MintFederationJWT` signs with `s.tls.LongTermPriv()` —
  but JWT alg defaults to RS256 (per Epic 10). If long-term is Ed25519
  (assumed throughout this plan), then `EdDSA` is the alg. Pin the alg
  explicitly.
- §6 acknowledges that long-term key rotation requires re-pairing all
  federations. This is an operational footgun; an EC table entry mentions
  it but no automatic warning fires. At least add a CLI check ("`maktaba-api
  keys rotate`" should warn if any federation is active).
- §3.3 SAS rendering uses 4 PGP words (32 bits). 32-bit space resists
  random collisions but not online attacks against active pairings. The
  story explicitly accepts this; the plan doesn't restate the budget. Worth
  an explicit "SAS provides ~32 bits of attacker-effort" comment.

---

## 4. Epic 16 — Subscriptions & Monetization (8 plans)

The cross-cutting items §1.3 (Ed25519 mis-attribution) and §1.4 (audit
categories) and §1.8 (new routes) cover the largest items. Plan-specific:

### 4.1 [plan-16-01](specs/epics/16-subscriptions/plan-16-01-free-tier.md) — generally clean

- Line 13: sentinel UUID `00000000-0000-0000-0000-000000000001` is
  introduced "per Epic 10 Story 10.9". Confirm Epic 10 actually creates this
  row in `users` at migration time (the previous review §1 didn't flag it
  missing, but worth a search).
- §3 "Migrating premium-back-to-free": data preserved, UI hides. Concrete
  consequence: if a `home`-tier user with 4 active users downgrades to
  `free` (1-seat), the 4 existing users keep working **read-only** per
  plan-16-04 §EC "Seats=4 but 5 users exist" — but plan-16-01's text says
  "free tier resumes" with no mention of the read-only state. Reconcile.

### 4.2 [plan-16-02](specs/epics/16-subscriptions/plan-16-02-premium-features.md) — multiple issues

- Lines 41-43: SQL function `current_seat_count()` is `STABLE`, but it
  references `now()` indirectly through nothing — actually it doesn't
  reference now(). It is `STABLE`. Fine. **However**, the function
  literally `SELECT count(*)::int FROM users WHERE id != '...'` — this
  works on Postgres but in SQLite the syntax `CREATE OR REPLACE FUNCTION
  ... AS $$ ... $$ LANGUAGE sql` is not supported. The plan ships only one
  migration variant.
- Line 174-181: `tier_grace` table — singleton-ish but PK is
  `(started_at, prev_tier)` which can hold multiple rows. The
  `OnTierChange` upgrade branch deletes by `prev_tier` only, leaving stale
  rows for any other prev_tier. Needs a `DELETE FROM tier_grace` (no WHERE)
  on upgrade, or constrain to one row.
- Line 84: `GET /api/admin/relay/usage` — see §1.8. Architecture §9.7
  doesn't have it; plan owns it implicitly but the route isn't enumerated.
- §4.1 backup-encryption KDF: `kdfKey = HKDF(license.signature ||
  "backup")`. Using the Ed25519 signature as KDF input is unusual — the
  signature is *public* (anyone with the license file has it). The plan
  reasons this is fine because "loss of license = loss of backup". But the
  encryption then offers no confidentiality against any attacker who has
  the license file (which is itself shipped to customers). Either include a
  user-supplied passphrase in the KDF, or document explicitly that the
  encryption is integrity-only.

### 4.3 [plan-16-03](specs/epics/16-subscriptions/plan-16-03-subscription-management.md) — multiple issues

- `billing_subscriptions` (lines 56-64) has no FK to `billing_customers`;
  `stripe_customer` is a TEXT column with no `REFERENCES`. Orphan rows on
  customer delete are possible. Add FK + ON DELETE CASCADE.
- `billing_invoices` (lines 67-75) similarly has no FK to `billing_customers`.
- Line 142-144: dispute handler updates `billing_subscriptions.tier =
  'free'` but doesn't trigger the license rotation flow that the rest of the
  plan relies on (lines 150). A flipped `tier` in the local table without a
  fresh license = the resolver still reads the old license. The dispute
  branch is incomplete.
- Line 119: `webhook.ConstructEvent(body, ...)` validates Stripe's signature.
  Good. The 1 MB cap (line 118) is reasonable but Stripe's max payload is
  ~256 KB; the cap is roomy but the signature check happens *after* reading
  to memory — fine for 1 MB but worth a comment.
- §10 EC "User deleted in our DB but lingering Stripe customer" — the
  reconciliation code (§5) iterates *all* Stripe subscriptions; for orphan
  customers this works, but plan-16-03 hard-codes
  `Status: stripe.String("all")` — should be `active|past_due|canceled` to
  bound the iteration.

### 4.4 [plan-16-04](specs/epics/16-subscriptions/plan-16-04-license-validation.md) — beyond §1.3

- Line 92: `licenses.raw TEXT NOT NULL` — the full license JSON including
  signature is stored as plaintext. The story AC says "License keys are
  never logged or returned by `/api/settings`". The DB row is technically
  not a "log" or "response", but operators backing up Postgres dumps will
  have it. Consider encrypted-at-rest via Epic 10 Story 10.14's data key.
- Line 88: `licenses` is a singleton (`id SMALLINT PRIMARY KEY DEFAULT 1
  CHECK (id=1)`). Plan-16-08 §1 mentions cache key
  `(user_id, license_state_version)` — `license_state_version` is not a
  column on `licenses`. Either add `version SERIAL` or use `last_refreshed_at`
  as the version. plan-16-08 needs alignment.
- Line 220-233: clock-rewind defense uses `effectiveNow = max(local now,
  last_refreshed_at)`. This is correct for *expiry* but if `last_refreshed_at`
  is in the past (license server unreachable for days), it's not a defense
  — it's the original timestamp. The actual defense is "the license server's
  Date header" (line 222) but the implementation isn't shown. Add the
  HTTP-Date validation logic.

### 4.5 [plan-16-05](specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md) — generally clean

- §4 declares `Events` constants client-side (16-05) and an `Allowed` table
  server-side (16-07); these are two sources of truth for the same
  vocabulary. Drift between releases will silently produce 400
  `unknown-event-kind` errors. Either generate one from the other, or
  document a release-coordination convention.
- §3 "rotated on opt-out and back-in" — `getOrCreatePseudonym` only
  generates if absent; the plan needs an explicit `clearPseudonym` call on
  opt-out. The implementation at line 73 does `localStorage.getItem(KEY)`
  but the rotation case (test `TestPseudonymRotatesOnOptOutOptIn`) requires
  explicit removal that isn't shown.

### 4.6 [plan-16-06](specs/epics/16-subscriptions/plan-16-06-feature-flags.md) — clean modulo §1.3

- §3 line 119: `verifyEd25519` references `SERVER_LONGTERM_PUBKEYS` —
  bundled at build time. Per §1.3 this assumes Ed25519 keys exist in Epic
  10, which they don't. Plan-16-06 *needs* Ed25519, period; the prior-key
  bundling story is otherwise sound.
- §6 "tampering defense": `flagsClient.bundleSignatureValid()` is called on
  every privileged action. This is good but defeats the cache benefit (every
  privileged action does Ed25519 verify, ~100 µs each). Cache the verified
  signature for the bundle's lifetime.

### 4.7 [plan-16-07](specs/epics/16-subscriptions/plan-16-07-telemetry-api.md) — beyond §1.13

- §4 redactor rebuilds on `library_added`/`library_removed` events (line
  152). But Epic 9 plan-09-15 ships `library.deleted` (§4.4 of the prior
  review); the channel name here doesn't match. Pick `library.added` /
  `library.removed` consistently across plans.
- Line 188-211: inserts assemble `rows` then call `s.InsertEvents(... rows)`.
  The "atomic batch" claim (line 198 comment + `TestPostEventsAtomic`)
  requires a transaction; `InsertEvents` interface isn't shown. Pin the
  atomicity guarantee.
- §6 IP scrubbing: the CI test `TestNoRemoteAddrInTelemetryEvents` is a
  grep test. A grep on source is brittle — if a future PR uses
  `req.RemoteAddr` for an unrelated middleware-emitted log line, the test
  fails. Stronger: a static-analysis rule scoped to the telemetry insert
  path, or a runtime test that sends a request and asserts no IP appears
  in the row.
- Line 65: `metric TEXT NOT NULL CHECK (metric IN ('LCP','FID','CLS','INP','TTFB'))`
  — `FID` is **deprecated** as of Web Vitals v3 (replaced by INP). Either
  drop FID or document that it's accepted for legacy clients.

### 4.8 [plan-16-08](specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md) — multiple issues

- `feature_flag_overrides.scope_value TEXT` (line 53) — for `scope=tier`,
  the value is `'free'`/`'home'`/`'pro'`; for `scope=user`, a UUID; for
  `scope=cohort`, a cohort name. No CHECK; type-erased. Add a partial
  index per scope or document the convention.
- §4 `Resolve` does **3 DB roundtrips** per cache miss (tier overrides,
  cohorts, user overrides). At cold-cache start this multiplies by N users.
  Either fold into one query with `UNION ALL` or accept the 3 RTs and pin
  in tests. The cache is supposed to absorb this but the LRU + LISTEN-purge
  combo means a NOTIFY storm wipes the cache and forces N misses.
- Line 70-80: `notify_flags_changed()` `pg_notify('flags_changed',
  NEW.flag_key)` — `NEW` is undefined for DELETE triggers; the trigger
  declares `AFTER INSERT OR UPDATE OR DELETE`. On DELETE, `OLD` should be
  used, or two triggers split.
- Line 195: "the client bundles **both** active and previous public keys
  for a 7-day overlap; after that the previous key is dropped from the
  bundle in the next client release." The "next client release" cadence is
  not 7 days for native apps (App Store / Play review); a 7-day cap is
  unrealistic. Either bundle the rotation list dynamically (defeats the
  static-bundling story) or extend the overlap to ≥ 30 days.

---

## 5. Epic 17 — UX Design System (11 plans)

Strongest of the four epics. Story → plan AC traceability is tight; RTL,
reduced motion, and a11y are taken seriously throughout.

### 5.1 [plan-17-01](specs/epics/17-ux-design-system/plan-17-01-design-tokens.md) — beyond §1.11

- §2 declares Style Dictionary platforms `"swift/tvos"` and
  `"kotlin/androidtv"` but no platform for **iOS mobile** (Capacitor) or
  **desktop** (Tauri). Story 17.1 AC mentions iOS / Android / TV outputs;
  the plan only ships TV. Either iOS mobile reuses the tvOS output (size
  unit: `pt`), or a separate `swift/ios` platform is added.
- Line 167: AndroidTV `lightColors()` / `darkColors()` — but the
  `kotlin/object` format Style Dictionary 4.x emits is a flat `object`, not
  Compose `Colors`. Either use a custom format
  (`format: 'kotlin/compose-colors'`) or document the manual translation
  layer.
- Line 132: minor — `tokens.deprecated.json` "shim" is a great idea but the
  plan has no ECs for what happens when the shim itself drifts. Add an
  invariant test: every key in `deprecated.json` either resolves to a key
  that *exists* in `tokens.json` or warns at build.

### 5.2 [plan-17-02](specs/epics/17-ux-design-system/plan-17-02-component-library.md) — beyond §1.10

- Line 91: `<Modal>` props use `preventClose` (boolean) — but `@reach/dialog`
  uses `dangerouslyBypassFocusLock`-style options for this; the prop name is
  invented. With Radix UI Dialog (the recommended replacement, §1.10), the
  equivalent is `onInteractOutside={(e) => e.preventDefault()}` +
  `onEscapeKeyDown`. Renaming the prop: `dismissable={false}` is the common
  convention.
- Line 144: `inThemeProvider.current` is referenced without being declared.
  The dev-warn idiom is sound but the implementation is incomplete.
- §1 inventory enumerates 25 components; the file structure at §1 lists 20
  files — the inventory and structure are slightly out of sync (Drawer is
  in the list but no `Surface/Drawer.tsx` line item).

### 5.3 [plan-17-03](specs/epics/17-ux-design-system/plan-17-03-motion.md) — clean

One observation: §4 frame-rate adaptation references
`navigator.deviceMemory`. This API is non-standard (only Chromium); Safari /
Firefox return `undefined`. The fallback (no clamp) is fine but should be
explicit.

### 5.4 [plan-17-04](specs/epics/17-ux-design-system/plan-17-04-loading-states.md) — clean modulo a hook bug

- §1 `useDelayedSkeleton`: the effect's dep array is `[state.kind]` but the
  effect references `phase`, which is a stale closure capture. When `phase`
  changes between renders the effect doesn't re-run. The 200 ms minimum
  hold relies on `phase === 'skeleton'` at the moment success arrives; with
  a stale closure this can be off-by-one. Either include `phase` in deps or
  refactor with `useReducer`.

### 5.5 [plan-17-05](specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md) — clean

One nit: §1 classifier returns `t('error.network.title')` — `t` is called
*outside* a React render context. If the i18next instance isn't initialized
when the classifier first runs (e.g., classifying an error during boot before
i18n loads), you get the key back instead of the translation. Add a guard or
delay-translate via a render-time wrapper.

### 5.6 [plan-17-06](specs/epics/17-ux-design-system/plan-17-06-onboarding.md) — multiple issues

- §2 introduces 7 new endpoints under `/api/setup/*` and 2 under
  `/api/libraries/*` (probe, init-default). None are owned by Epic 7;
  see §1.8.
- Line 5 references "Epic 9" for library config but the actual library probe
  endpoint owner isn't pinned. Library scan is in Epic 9 Story 9.6 (manual
  scan); a `probe` step (returns size, count) is new — needs an owner.
- §6 STT auto-detect: "On Apple Silicon CI runner, `mlx` is in candidates"
  — the `whisper-cpu` always-included AC is fine, but the actual probe
  implementation references `api/internal/transcribe/probe.go` (line 13) —
  the `transcribe` package lives in **Pipeline** (Python) per architecture
  §1.2/3.4, not API (Go). The endpoint should be in API but the *probe*
  needs Pipeline RPC. The plan has neither side wired.
- Line 28: `current_step BETWEEN 1 AND 4` but the wizard has 4 steps + a
  tour. If a future story adds a step 5, the CHECK breaks.

### 5.7 [plan-17-07](specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md) — clean

- Line 50: `play.svg` vs `play-rtl.svg` — the play triangle is a graphic
  symbol that points to "next" (not "right"). Most major apps **do not**
  mirror the play icon in RTL (Netflix, YouTube, etc.); the plan choosing to
  do so is a deviation. Worth a note in `design/docs/rtl.md` for the
  rationale.
- §2 `useDirection` reads from a React context that mirrors `<html dir>`.
  Implementation not shown; fine if it's well-established but doesn't
  resolve the problem of native (tvOS / AndroidTV) needing the same
  primitive — they read from the platform `layoutDirection`, which can
  drift from the user's selected direction (e.g., user sets app dir to
  RTL but device locale is English).

### 5.8 [plan-17-08](specs/epics/17-ux-design-system/plan-17-08-player-controls.md) — clean modulo cross-references

- §1 mini-player decision is a real architecture choice. The 5-min
  idle-destroy fallback is good. One missing piece: when navigating to a
  *different* video, the persisted instance must reconfigure source — the
  plan doesn't address `replaceCurrentItem` semantics.
- §6 subtitle styling reads `subtitle.size` ∈ `{sm, md, lg, xl}` but
  plan-17-11 §5 reuses these "via the same CSS custom properties". Both
  plans mutate the same `--sub-*` vars; pin which component owns the
  initial set (probably 17-08; 17-11 just consumes).

### 5.9 [plan-17-09](specs/epics/17-ux-design-system/plan-17-09-search-results.md) — clean

- Line 48-49: `<Link to=…>` then immediately a `<button onClick=…>` inside
  it (`<TimestampChip>` at line 53). Nesting interactive elements is
  invalid HTML and confuses screen readers. Either pull TimestampChip out
  of the Link, or render the head as a non-Link `<article>` with a separate
  "Watch" button.
- `<Bidi dir="ltr">[{children}]</Bidi>` (line 86): the brackets aren't
  required for bidi isolation, the inner content is (the time string).
  Wrapping `[12:34]` doesn't *hurt* but the brackets get isolated too,
  which can produce unexpected punctuation flow in Arabic. Prefer
  `[<Bidi dir="ltr">{time}</Bidi>]`.

### 5.10 [plan-17-10](specs/epics/17-ux-design-system/plan-17-10-processing-progress.md) — clean modulo §1.9

The 7-stage strip is consistent and the canonical resolution per §1.9. No
plan-specific issues beyond that.

### 5.11 [plan-17-11](specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md) — clean modulo two bugs

- §3 `binarySearchSegment` returns `Math.max(0, lo - 1)` on no-match. When
  the player time is past the last segment's end (typical for the last few
  seconds of a video), `lo` is `segments.length`, so the return is
  `segments.length - 1` — the last segment is "current" forever. Either
  return `-1` (and have `useCurrentSegment` honor that) or accept the
  behavior and document.
- §1 `itemSize={(i) => estimateHeight(segments[i])}` references
  `estimateHeight` which is not defined anywhere in the plan. Concrete
  formula needed; otherwise the `VariableSizeList` will use defaults and
  lose the auto-scroll precision.

---

## 6. Plans by quality

**Clean / minor notes only (3 plans):**
- 17: [17-03](specs/epics/17-ux-design-system/plan-17-03-motion.md), [17-04](specs/epics/17-ux-design-system/plan-17-04-loading-states.md), [17-05](specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md).

**Substantially clean (cross-cutting only):**
- 14: [14-03](specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md), [14-04](specs/epics/14-tv-apps/plan-14-04-voice-search.md), [14-06](specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md).
- 15: [15-01](specs/epics/15-discovery/plan-15-01-mdns.md), [15-05](specs/epics/15-discovery/plan-15-05-qr-pairing.md).
- 16: [16-01](specs/epics/16-subscriptions/plan-16-01-free-tier.md), [16-05](specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md).
- 17: [17-01](specs/epics/17-ux-design-system/plan-17-01-design-tokens.md), [17-02](specs/epics/17-ux-design-system/plan-17-02-component-library.md), [17-07](specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md), [17-08](specs/epics/17-ux-design-system/plan-17-08-player-controls.md), [17-09](specs/epics/17-ux-design-system/plan-17-09-search-results.md), [17-10](specs/epics/17-ux-design-system/plan-17-10-processing-progress.md), [17-11](specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md).

**Significant fixes needed:**
- 14: [14-01](specs/epics/14-tv-apps/plan-14-01-tvos.md), [14-02](specs/epics/14-tv-apps/plan-14-02-android-tv.md), [14-05](specs/epics/14-tv-apps/plan-14-05-continue-watching.md), [14-07](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md).
- 15: [15-02](specs/epics/15-discovery/plan-15-02-cloud-relay.md), [15-03](specs/epics/15-discovery/plan-15-03-federation.md), [15-04](specs/epics/15-discovery/plan-15-04-dlna-upnp.md).
- 16: [16-02](specs/epics/16-subscriptions/plan-16-02-premium-features.md), [16-03](specs/epics/16-subscriptions/plan-16-03-subscription-management.md), [16-04](specs/epics/16-subscriptions/plan-16-04-license-validation.md), [16-06](specs/epics/16-subscriptions/plan-16-06-feature-flags.md), [16-07](specs/epics/16-subscriptions/plan-16-07-telemetry-api.md), [16-08](specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md).
- 17: [17-06](specs/epics/17-ux-design-system/plan-17-06-onboarding.md).

**Schema/contract-incompatible (cannot ship in current form):**
- [plan-15-06](specs/epics/15-discovery/plan-15-06-pairing-api.md) (double-owns pairing endpoint with plan-10-17, see §1.2; also has a precedence bug in nonce check, §3.6).
- [plan-14-07](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) (double-owns `/api/recommendations` with plan-07-21, see §1.1; also references undefined `playback_state.duration_sec` and `media_features` table).

**Plans that are too thin:**

None of the 33 plans are too thin. The shortest plans
([plan-16-01](specs/epics/16-subscriptions/plan-16-01-free-tier.md):180 lines,
[plan-17-08](specs/epics/17-ux-design-system/plan-17-08-player-controls.md):225 lines)
are concise relative to scope. One sub-section is thin:

- [plan-15-04 §6 byte server](specs/epics/15-discovery/plan-15-04-dlna-upnp.md)
  — `serveFile` is one function; security-sensitive (path traversal) but
  the test plan only has one test. Expand with sandbox-root anchoring +
  symlink resolution rules.

---

## 7. Recommended action order

Roughly in priority order:

### 7.1 Architecture decisions (block everything else)

1. Resolve §1.1 — pick one implementation for `/api/recommendations`
   (recommend plan-14-07's JSONB-cache shape; rewrite plan-07-21 to consume
   it).
2. Resolve §1.2 — pick one implementation for `/api/auth/pair*` (recommend
   merge: plan-15-06's nonce + plan-10-17's `code_hash` + state enum). Pick
   one migration number.
3. Resolve §1.3 — own Ed25519 long-term key generation in Epic 10 (new
   story or extend 10.6). Until then plan-15-07, plan-16-04, plan-16-06,
   plan-16-08 cannot be implemented.
4. Resolve §1.4 — extend `audit_log.category` enum (or drop the CHECK)
   in plan-09-17.
5. Resolve §1.5 — decide whether `playback_state.duration_sec` is added
   (own in plan-07-11) or whether plans 14-05/14-07 JOIN `videos`.
6. Resolve §1.6 — `videos.poster_url` and `videos.deleted_at` (the prior
   review §1.1 already flagged; still unresolved).
7. Decide on `subtitle_gen` stage canonicalization (§1.9) — update
   architecture §3.5.
8. Decide on Reach UI replacement (§1.10) — Radix UI Dialog.
9. Decide on `pgvector` extension (§1.12) — add to architecture §2.1 or
   redesign plan-14-07's neighbor lookup.
10. Pick a relay-edge ownership model (§3.2) — first-party service vs.
    third-party tunnel. Currently architecture §1.4 doesn't acknowledge a
    fourth service.

### 7.2 Single-owner schema sweeps

11. Owner for `media_features` table — referenced by plan-09-10 and
    plan-14-07; nobody ships the migration.
12. Owner for `recommendation_runs` / `recommendation_dismissals` (after §1.1
    resolved).
13. Renumber migrations to remove the 0040 conflict (§1.7) and align Epic
    14-17 numbering with Epic 10/12.
14. Owner for the static-file route serving `apple-app-site-association`,
    `assetlinks.json`, and the new federation discovery doc (§1.8).

### 7.3 Critical compile / correctness bugs

15. [plan-15-06:211](specs/epics/15-discovery/plan-15-06-pairing-api.md)
    nonce check precedence bug (`!ConstantTimeCompare(...) == 1`).
16. [plan-14-02:91](specs/epics/14-tv-apps/plan-14-02-android-tv.md) Apollo
    plugin id `apollo3` → `apollo`.
17. [plan-14-04:62-63](specs/epics/14-tv-apps/plan-14-04-voice-search.md)
    `await SFSpeechRecognizer.requestAuthorization()` shape (needs
    continuation wrapper).
18. [plan-17-01:25-30](specs/epics/17-ux-design-system/plan-17-01-design-tokens.md)
    add `color.neutral.*` group (build will fail; §1.11).
19. [plan-17-04 useDelayedSkeleton](specs/epics/17-ux-design-system/plan-17-04-loading-states.md)
    stale-closure on `phase` in useEffect.
20. [plan-17-11 binarySearchSegment](specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md)
    boundary-of-end-of-video bug.
21. [plan-17-11 estimateHeight](specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md)
    referenced but undefined.
22. [plan-16-08:71-78](specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md)
    `NEW` undefined in DELETE branch of trigger.
23. [plan-14-05 partial index + deleted_at + duration_sec](specs/epics/14-tv-apps/plan-14-05-continue-watching.md)
    rewrite per §1.5/1.6.
24. [plan-14-07 singleflight error](specs/epics/14-tv-apps/plan-14-07-recommendations-api.md)
    handling.

### 7.4 Cross-plan coordination

25. Endpoint name fixes (§2.1, §2.2): `/api/playback/state` →
    `/api/me/playback-state` or `/api/stream/sessions/{id}/progress`. Pick
    one; align with prior review §1.11.
26. Extend `audit_log.category` enum to include `pair`, `federation`,
    `flags`, `subscription`, `device` (§1.4).
27. Stage list canonicalization across Epic 17 / Epic 9 / Epic 11 (§1.9).
28. Foreign keys on Stripe-related tables (§4.3).
29. `tier_grace` upgrade-cleanup bug (§4.2).
30. Two-source-of-truth telemetry vocabulary (§4.5).
31. Compose `lightColors()`/`darkColors()` Style Dictionary format (§5.1).

### 7.5 Smaller polish

32. mDNS server library decision (`grandcat/zeroconf` vs `hashicorp/mdns`)
    (§3.1).
33. `relay-agent` JCS canonicalization for cert-rotation signing (§3.2).
34. Federation JWT alg explicit choice (RS256 vs EdDSA) (§3.3).
35. plan-16-04 license-server `Date` header validation (§4.4).
36. plan-16-08 RT amplification on cache-miss (§4.8).
37. plan-17-09 nested-interactive-element a11y (§5.9).
38. plan-15-04 byte-server symlink hardening (§6).
39. Backup-encryption KDF user-passphrase decision (§4.2).

---

## Appendix A — Files reviewed

- `specs/architecture.md` (2,292 lines).
- `specs/epics/14-tv-apps/`: 7 plans + 7 stories + README.
- `specs/epics/15-discovery/`: 7 plans + 7 stories + README.
- `specs/epics/16-subscriptions/`: 8 plans + 8 stories + README.
- `specs/epics/17-ux-design-system/`: 11 plans + 11 stories + README.

Cross-referenced against:

- `specs/epics/07-api-server/plan-07-04-video-crud.md`,
  `plan-07-11-watch-progress-sync.md`, `plan-07-21-recommendations.md`.
- `specs/epics/09-library-management/plan-09-17-library-audit.md`.
- `specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md`,
  `plan-10-17-auth-pair.md`.
- `specs/epics/12-mobile/plan-12-10-device-registration-api.md`.
- `specs/PLAN_REVIEW_07_13.md`.

Total: 70 specification files plus the architecture document and the prior
review.

## Appendix B — Reviewer breakdown by issue type

| Issue type | Total instances | Most-affected epics |
|------------|-----------------|---------------------|
| Endpoint double-ownership | 2 | 14↔07, 15↔10 |
| Schema column drift (poster_url, deleted_at, duration_sec, codec) | 8 | 14, 15 |
| Schema table drift (media_features, library.roots, audit_log enum) | 6 | 14, 15, 16 |
| Migration number conflict | 2 | 14↔12, 14↔internal |
| Misattributed Ed25519 vs RS256 | 4 | 15, 16 |
| Compile-bug-grade Go errors | 3 | 15, 16, 14 |
| Compile-bug-grade JS/Kotlin errors | 4 | 14, 17 |
| Tauri/Compose/Apollo plugin or class errors | 2 | 14 (Apollo), 17 (Compose) |
| Reach UI deprecation | 1 | 17 |
| Story↔plan deliberate deviation | 4 | 15, 17 |
| New routes outside architecture §9 | 18 | 14, 15, 16, 17 |
| Audit category enum mismatch | 4 | 15, 16 |
| Singleton-row pattern proliferation | 5 | 15, 16, 17 |
