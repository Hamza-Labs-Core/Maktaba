# Implementation Plan Review — Epic 25 (Cloud Relay)

**Scope.** All 36 implementation plans paired with their stories at
[`specs/epics/25-cloud-relay/`](epics/25-cloud-relay/) on
`main` (commit `bcc0718`, PR #12).

**Method.** Each plan reviewed against:
- [`specs/architecture.md`](architecture.md) §13 (Cloud Relay
  architecture; canonical for the WSS frame format, role split,
  trust boundary, entitlement, billing, push, and distribution).
- [`specs/epics/25-cloud-relay/README.md`](epics/25-cloud-relay/README.md)
  §"Migrations claimed by this epic" (slot reservations 0001–0010).
- The matching story acceptance criteria.
- The current implementation on `main` (especially the local
  server's `api/`, `shared/db/migrations/`, and Epic 10/15/16/21
  plan files for cross-epic references).
- [`PLAN_REVIEW_18_24.md`](PLAN_REVIEW_18_24.md) for inherited
  cross-cutting issues (Go module structure, audit-log shape, top-level
  module assumption, secrets registry).

**Verdict at a glance.**

| Bucket | Count | Plans |
|--------|-------|-------|
| BLOCKED | 4 | 25.02, 25.06, 25.07, 25.10 |
| NEEDS_REVISION | 22 | 25.01, 25.03, 25.04, 25.05, 25.08, 25.09, 25.11, 25.12, 25.13, 25.14, 25.16, 25.17, 25.20, 25.21, 25.22, 25.23, 25.24, 25.25, 25.26, 25.30, 25.34, 25.35 |
| PASS (ship as-is) | 10 | 25.15, 25.18, 25.19, 25.27, 25.28, 25.29, 25.31, 25.32, 25.33, 25.36 |

Four BLOCKED plans gate the whole epic: a **two-way migration-filename
collision** (§1.1), a **missing-but-load-bearing Epic 10 Story 10.18**
(§1.2), and the **server-side cloudlink Go module is unspecified**
(§1.3). The remaining "NEEDS_REVISION" plans are smaller fixes —
duplicate column declarations across plans, mismatched table-column
names, story-number drift, and a few cases where a plan claims an
ownership the README assigns to another story.

---

## 1. Top-priority cross-cutting issues

### 1.1 [BLOCKING] Migration filename collisions inside `cloud/migrations/`

The plans use an `00<slot><seq>_<topic>.sql` filename convention
(plan-25-01:58) but several pairs disagree on which slot owns which
table. Two filename collisions and two slot-owner contradictions exist:

| Filename | Plan A | Plan B |
|----------|--------|--------|
| `00020001_*` | plan-25-02 §1 (`cloud_users`, `cloud_sessions`, `cloud_password_resets`, `cloud_jwks`, `cloud_audit`) | plan-25-06 §1 (`cloud_servers`, `cloud_server_tokens`, `cloud_claim_tokens`) |
| `00020002_*` | plan-25-03 §1 (`cloud_identities`, `cloud_oauth_merge_tokens`) | plan-25-10 §1 (`cloud_server_endpoints`) |

Per [`README.md`](epics/25-cloud-relay/README.md) §"Migrations claimed
by this epic":

| Slot | Owning story | Tables |
|------|--------------|--------|
| 0001 | 25.1 | `cloud_users`, `cloud_identities`, `cloud_sessions` |
| 0002 | **25.6** | `cloud_servers`, `cloud_server_tokens`, `cloud_claim_tokens` |
| 0006 | **25.20** | `cloud_audit` |
| 0009 | 25.5 | "indexes for GDPR-export queries" |

Three contradictions with the README:

1. plan-25-02 creates the user/session tables in slot 0002, but
   README assigns slot 0001 to that story. plan-25-01:51 plans a
   placeholder `00010001_users_sessions.sql` for "real DDL lands in
   25.2" — so 25.2's migration should be `00010001_*`, not
   `00020001_*`. As shipped, plan-25-02 races plan-25-06 for the same
   filename.
2. plan-25-02 also CREATES `cloud_audit` (line 79), but README
   assigns `cloud_audit` to slot 0006 (25.20). plan-25-20 then ALTERs
   `cloud_audit` (line 20), correctly assuming it exists, but
   contradictory ownership means a clean re-run of slots 0001→0006
   will fail (`CREATE TABLE` collision when 25.20 also tries to
   create it).
3. plan-25-03 creates `cloud_identities` in slot 0002, but README
   assigns that table to slot 0001 (25.1).

plan-25-10 is not assigned any slot at all in the README; it
opportunistically grabs `00020002_*`, collides with plan-25-03, and
diverges from the README's promised 0001–0010 inventory.

plan-25-05 separately uses `00090009_*` (line 18) — 4 digits past
slot 0009. If the convention is `slot+seq`, that names "slot 0009,
sequence 0009". There are no other entries in slot 0009; 0009 should
be the first sequence. Inconsistent with every other plan's `<slot>0001`
opener.

**Recommendation.** Land a single PR that:

- Re-files plan-25-02 as `00010001_email_auth.sql` (matches README
  slot 0001).
- Re-files plan-25-03 as `00010002_oauth_identities.sql`.
- Re-files plan-25-04 as `00010003_apple_oauth.sql`.
- Removes `cloud_audit` from plan-25-02's CREATE list; require
  plan-25-02 (and any earlier plan) to **defer** audit writes until
  slot 0006 has applied. Either:
  - Stub `cloud_audit` writes behind a feature flag that defaults
    to no-op when the table is missing, or
  - Have plan-25-02 carry an `IF NOT EXISTS` create with the
    plan-25-20 schema, and have plan-25-20 only carry the column
    additions (`is_admin`, `reason`) + trigram indexes.
- Re-files plan-25-05 as `00090001_account_profile.sql`.
- Re-files plan-25-10 as `00020002_server_endpoints.sql` only if
  plan-25-03 moves into slot 0001 (per above). Otherwise assign
  plan-25-10 to a new slot — recommendation: `00020002` after
  plan-25-03 is re-filed.
- Re-files plan-25-06 unchanged (`00020001_servers_and_claims.sql`).
- Adds a `cloud/migrations/MANIFEST.md` mirroring
  `shared/db/migrations/MANIFEST.md` (Epic 22 pattern) so a CI gate
  can detect file/slot/owner drift before it lands.

### 1.2 [BLOCKING] Missing dependency: Epic 10 Story 10.18 (Ed25519 server identity)

plan-25-06:11 declares the cloud's claim-token flow depends on
"Ed25519 from Epic 10 Story 10.18 (server identity). Reused
unchanged." plan-25-07:412 (Dependencies §10) similarly lists
"Epic 10 Story 10.18 (server identity Ed25519)."

**Story 10.18 does not exist.** [`specs/epics/10-auth-security/`](epics/10-auth-security/)
ends at plan-10-17. [`epics/15-discovery/plan-15-07-federation-api.md:16`](epics/15-discovery/plan-15-07-federation-api.md)
already flagged the gap explicitly:

> "This plan depends on a new Epic 10 Story 10.18 ('Ed25519
> long-term server identity keys') that owns the generation,
> rotation, and `kid`-indexed publication of the long-term keypair.
> **Until 10.18 lands this plan blocks.**"

`grep -rn "ed25519\|Ed25519" api/internal/ shared/db/migrations/`
confirms the only existing Ed25519 use is licenses (Epic 16 Story
16.4). There is no long-term server-identity keypair, no storage
schema, no rotation policy, no JWKS, no kid-indexed publication
anywhere on `main`.

Consequences for Epic 25:

- plan-25-06 §1 stores `cloud_servers.server_pubkey BYTEA NOT NULL
  -- 32-byte Ed25519`. The cloud cannot verify a pubkey it never
  received; without 10.18 there's no generator producing the keypair
  on the server side and no source for `server_pubkey`.
- plan-25-06 §3 says the **server** posts `{token_hash,
  server_pubkey}` to `/api/servers/claim/init`. Server-side code to
  generate, persist, and load that Ed25519 keypair is owned by
  10.18.
- plan-25-07 §6 storage uses `secrets.SealedBox` from Epic 10.14,
  but the Ed25519 *keypair* itself must be ed25519-generated and
  stored — that's 10.18, not 10.14 (which is generic secret
  loading, plan-10-14 exists).
- plan-25-26 (entitlement signing) is **cloud-side** Ed25519 keys,
  not server-side — so 25.26 itself is not blocked, but a server
  cannot verify the entitlement against `Sub=server_id` if the
  server has no stable identity in the first place. The "binds to
  server_id" defense (25.26 EC `TestReplayAcrossServers`)
  presupposes a server identity.

**Recommendation.** Either:

1. Add an Epic 10 story-10-18 + plan-10-18 that owns local-server
   Ed25519 identity (generate-on-first-boot, sealed-at-rest via
   plan-10-14, kid-rotation policy, JWKS-style local publication so
   clients can pin the cert), and re-anchor plan-15-07, plan-25-06,
   plan-25-07's dependency references on it; or
2. Fold the server-identity Ed25519 generation into plan-25-06 §3
   ("Server-side companion") explicitly: own the keygen, sealing,
   and persistence in this plan. Update plan-25-06 §0 dependency
   table to drop the 10.18 reference; cross-mention plan-15-07.

Option 1 is the cleaner factoring because federation (15.7) and
cloud-claim (25.6) need the **same** key. Option 2 ships faster.
Pick one before any of 25.06/25.07/15.07 can ship.

### 1.3 [BLOCKING] Server-side cloudlink Go module placement is undeclared

plan-25-07 puts `cmd/maktaba-cloudlink` and `internal/cloudlink/*`
inside the local-server repo (alongside `api/`, `streaming/`). The
plan does not say which Go module owns these directories.

The actual `main` layout (verified at `find . -name go.mod`):

- `api/go.mod` (module `github.com/Hamza-Labs-Core/Maktaba/api`)
- `streaming/go.mod`
- `tools/{test-budget,healthcheck,release,migration-lint}/go.mod`
- `shared/{health,log,metrics,states,testtier,tracing}/go/go.mod`

There is **no top-level `go.mod`**.
[`PLAN_REVIEW_18_24.md` §1.9](PLAN_REVIEW_18_24.md) already flagged
that Epic 22 plans assume one and the assumption never landed.

Specific consequences:

- plan-25-07 §1's tree (`cmd/maktaba-cloudlink/`, `internal/cloudlink/`)
  reads as top-level paths. To live there they would need a top-level
  `go.mod` — none exists.
- plan-25-07 §6 imports `secrets.SealedBox` from Epic 10.14, which
  lives in `api/internal/auth/keys/` (verified). A binary outside
  `api/`'s module cannot import `api/internal/...` (Go forbids
  cross-module `internal/` imports). So cloudlink either:
  - Lives under `api/cmd/maktaba-cloudlink/` (sharing `api/go.mod`),
    or
  - Has its own `cmd/maktaba-cloudlink/go.mod` and the sealing helper
    is promoted to a `shared/secrets/go/` module.
- plan-25-30 §1 (Docker) compiles `./cmd/maktaba-cloudlink`,
  `./cmd/maktaba`, `./cmd/pipeline-launcher` at the **top level**.
  None of those `cmd/*` directories exist on `main` (`find . -type d
  -name "cmd"` finds only `api/cmd`, `streaming/cmd`, etc.).
- plan-25-27 §1's Xcode project bundles `maktaba-cloudlink` next to
  `api`, `streaming`, `pipeline-launcher` — fine as a *binary
  layout*, but the source layout that produces those binaries is
  still undeclared.

**Recommendation.** plan-25-07 §1 must pin one of:

- Move cloudlink under `api/cmd/maktaba-cloudlink/` and
  `api/internal/cloudlink/`. Sealing helper stays at
  `api/internal/auth/keys/`. Simplest; no new module.
- Promote `internal/cloudlink/` to its own module
  `cloudlink/go.mod`; promote `auth/keys/sealedbox.go` to a shared
  `shared/secrets/go/` module with replace directives.

Either way, plan-25-07 §1 + plan-25-30 §1 + plan-25-36 §1 all need
the path normalized in one PR; otherwise the Docker image, the
desktop installers, and the uninstaller all reference paths that
don't compile.

### 1.4 [BLOCKING] Tier string vocabulary disagrees across four plans

Four sources of truth for the tier-string catalog:

| Source | Plan strings |
|--------|--------------|
| [`architecture.md:2779-2783`](architecture.md) | `Free`, `Pro`, `Family` (no monthly/yearly suffix) |
| [`epics/16-subscriptions/plan-16-04-license-validation.md:21`](epics/16-subscriptions/plan-16-04-license-validation.md) | `home` (and presumably others; story-16-04 sample uses `tier: "home"`) |
| plan-25-12:21–28, [plan-25-13:25](epics/25-cloud-relay/plan-25-13-stripe-checkout.md) | `free`, `pro_monthly`, `pro_yearly`, `family_monthly`, `family_yearly`, `suspended` |
| plan-25-26:74 | `Tier` field is `string`; the suspended-detect branch (`p.Tier == "suspended"`) implies the same vocabulary as 25.12 — but no enum is pinned |

[plan-25-26:225](epics/25-cloud-relay/plan-25-26-entitlement-signing.md)
"Tier strings catalog | Shared with Epic 16. | Cross-epic." declares
a sharing relationship that the codebase doesn't honor — 16.4 uses
`home`, 25.12 uses `pro_monthly`. A local server gating "cloud_relay"
on `tier=pro_monthly` will never match an Epic 16 license that says
`tier=home`.

**Recommendation.** Pin one tier-string vocabulary in
[`architecture.md` §13.10](architecture.md) and reference it from
plan-16-04 §1, plan-25-12 §1, plan-25-13 §2, and plan-25-26 §3.
Strong preference: introduce a `tier` enum +
`interval IN ('monthly','yearly')` column rather than a
`pro_monthly`/`pro_yearly` Cartesian product — matches the
architecture table at §13.10 ("Pro Y / Family Y | 17% off | same |
same | same") which treats interval as a *modifier*, not a separate
tier. Today's plans flatten interval into tier, and 25.26's verifier
will have to know about both representations.

### 1.5 [MAJOR] `cloud_audit` shape diverges from local `audit_log`; ownership unclear

Local server's `audit_log` (slot 0054, owned by plan-21-06; see
[`shared/db/migrations/0054_audit_log.sql`](shared/db/migrations/0054_audit_log.sql)
on `main`) has:

| Field | Type |
|-------|------|
| `id` | UUID PRIMARY KEY |
| `occurred_at` | TIMESTAMPTZ |
| `category` | TEXT CHECK (auth/library/admin/data/config/keys/device/security/integrity/subscription) |
| `action` | TEXT |
| `actor_user` | UUID |
| `actor_ip` | INET |
| `actor_source` | TEXT |
| `target_kind` | TEXT |
| `target_id` | TEXT |
| `payload` | JSONB |
| `error_id` | TEXT |

plan-25-02 §1 (`cloud_audit`) has:

| Field | Type |
|-------|------|
| `id` | BIGSERIAL PRIMARY KEY |
| `ts` | TIMESTAMPTZ |
| `actor_user_id` | UUID |
| `action` | TEXT |
| `target_type` | TEXT |
| `target_id` | TEXT |
| `ip` | INET |
| `ua` | TEXT |
| `payload` | JSONB |

Five name drifts (`id` type, `ts` vs `occurred_at`, `actor_user_id`
vs `actor_user`, `ip` vs `actor_ip`, `target_type` vs `target_kind`)
plus the cloud schema is missing `category`, `actor_source`,
`error_id`. Because the cloud is a *separate database*, no SQL
collision occurs — but the audit reader/writer/redactor/CSV-exporter
cannot be shared between the cloud and local server. PLAN_REVIEW_18_24
§1.4 spent a full subsection unifying the local audit shape; this
review recommends following that pattern in the cloud.

The README says slot 0006 / 25.20 owns `cloud_audit`. plan-25-02 §1
creates `cloud_audit` in slot 0002 (via §1.1 above) — a direct
ownership conflict.

**Recommendation.** Designate plan-25-20 as the canonical creator
of `cloud_audit`; have plan-25-02 drop the create. plan-25-20's
schema should mirror the local `audit_log` field-for-field (including
the category enum) so audit tooling is portable. Add `is_admin` and
`reason` columns there as plan-25-20 already plans. Adopt UUIDv7
PK (plan-25-20 §10 already notes the option).

### 1.6 [MAJOR] Inconsistent dependency: Story 15.2 vs 15.1 for mDNS

plan-25-10 §9 lists "Epic 15 Story 15.2 (LAN mDNS source)" as a
dependency. The actual story is:

- [`story-15-01-mdns.md`](epics/15-discovery/story-15-01-mdns.md) =
  "Local network discovery (mDNS / Bonjour)"
- [`story-15-02-cloud-relay.md`](epics/15-discovery/story-15-02-cloud-relay.md) =
  "Global discovery (optional cloud relay)" — itself a duplicate of
  the Epic 25 cloud-relay theme (separate ticket-design issue)

plan-25-10:174 "Watcher: subscribes to mDNS publisher (Epic 15.2)"
is the same wrong reference; the publisher lives in 15.1.

**Recommendation.** Replace plan-25-10's "15.2" references with
"15.1". Also flag the Epic 15 / Epic 25 cloud-relay overlap: Epic
15.2 reads as a v1-time stub that 25.7–25.10 now supersedes; that
story-level redundancy should be reconciled in Epic 15's README.

### 1.7 [MAJOR] `stripe_customer_id` ownership: declared twice

plan-25-02 §1 declares `cloud_users.stripe_customer_id TEXT` and
indexes it (`cloud_users_stripe_cust_idx`).
plan-25-13 §0 explicitly notes the column is "added in 25.2".

This is *not* a re-declaration in plan-25-13's migration; it's
self-consistent. But plan-25-13's migration body
(`00040001_billing.sql`) creates `cloud_subscriptions` with its
own column `stripe_customer_id` (line 24) — a second copy of the
same data, denormalized. The plan never addresses how the two are
kept in sync.

**Recommendation.** Drop `cloud_subscriptions.stripe_customer_id`
(plan-25-13 §1) and join through `cloud_users.stripe_customer_id`;
or keep the denormalization for fast lookup but add an
`ON UPDATE` trigger / explicit invariant test.

### 1.8 [MAJOR] `cloud_family_members` column name drift between plan-25-12 and plan-25-13

[plan-25-12:88](epics/25-cloud-relay/plan-25-12-tier-enforcement.md)
references `cloud_family_members(payer_id, member_user_id)`.
[plan-25-13:50](epics/25-cloud-relay/plan-25-13-stripe-checkout.md)
declares `cloud_family_members(payer_user_id, member_user_id, …)`.

The plan-25-12 prose says the table "lives in 25.13 migration", so
the canonical declaration is plan-25-13's. plan-25-12's family-member
resolver pseudocode references the wrong column name.

**Recommendation.** Update plan-25-12's family-member prose +
pseudocode to use `payer_user_id`. Add a test in plan-25-13 §8 that
inserts a row and reads it back via plan-25-12's resolver to prevent
re-drift.

### 1.9 [MAJOR] Token entropy: claim token math contradicts story

plan-25-06 §5 admits the arithmetic problem: "12 random bytes is
96 bits; base32 = 96/5 = 19.2 → 20 chars. We want 8 chars = 40 bits.
**Correct the design**: use 5 random bytes (40 bits → 8 base32
chars)…"

Story 25.6 and [`architecture.md:2722`](architecture.md) both say
"96-bit token, base32-encoded as `K3F9-MZ7P`" — that's 8 chars and
40 bits. plan-25-06 acknowledges the mismatch and decides to follow
40-bit math, then notes "coordinate with the story to update once."

**Recommendation.** Update story-25-06 and architecture.md §13.7 to
say "40-bit token, base32 8 chars (matching 10-min TTL + per-IP rate
limit threat model from plan-25-06 §5)." This is a doc reconciliation
that should land *in the plan-25-06 PR* so the story and plan don't
diverge across reviewers. The 40-bit-with-rate-limit math is sound
(see plan-25-06:189–190); the spec wording is just imprecise.

### 1.10 [MAJOR] Top-level Go module assumed by plan-25-30, plan-25-27, plan-25-34

Same issue as PLAN_REVIEW_18_24 §1.9, replayed in Epic 25:

- plan-25-30:31–34 (Dockerfile): `go build ... ./cmd/maktaba`,
  `./cmd/pipeline-launcher`, `./cmd/maktaba-cloudlink`. None of these
  paths exist on `main`. `api/cmd/api`, `streaming/cmd/streaming`
  exist (verified). A top-level `cmd/` does not.
- plan-25-27 §1 (`scripts/build.sh`) "vendor binaries" assumes those
  binaries built from somewhere.
- plan-25-34 (auto-update mechanism) downloads "the binaries"
  from `releases.maktaba.app/manifest.json`; manifest build
  pipeline is not specified.

**Recommendation.** Either land the top-level Go module + `cmd/`
tree as part of Epic 22 (plan-22-02 already overlaps), or rewrite
plan-25-30 §1's Dockerfile to build from `./api/cmd/api`,
`./streaming/cmd/streaming`, `./cloud/cmd/maktaba-cloud`. The latter
is closer to the current `main` layout and removes the need for a
fictitious top-level module.

### 1.11 [MAJOR] `cloud_jwks` table introduced in plan-25-02; conflicts with plan-25-26 `cloud_entitlement_keys`

plan-25-02:69 creates `cloud_jwks(kid, alg, public_pem,
private_pem_sealed, …)` for **JWT signing keys** (RS256, 90-day
rotation, story §9).
plan-25-26:21 creates `cloud_entitlement_keys(kid, public_pem,
private_pem_sealed, …)` for **entitlement signing keys** (Ed25519,
monthly rotation).

Two parallel keystores. Both publish a JWKS. Both rotate. Both
support intro chains. Both store sealed private keys.

**Recommendation.** Either:

- Keep them separate (current design) but rename plan-25-02's table
  to `cloud_jwt_keys` so reviewers don't conflate the two keystores
  with the `JWKS` naming.
- Unify into one `cloud_signing_keys` table with `purpose IN
  ('jwt','entitlement')` discriminator. Less duplication, one
  rotation cron, but admin operators must understand the discriminator.

This is a refactor-style issue, not a hard conflict. Lower urgency.

### 1.12 [MAJOR] Webhook secret rotation table mentioned but never declared

plan-25-14:44 says "`s.secrets.Active(ctx)` returns up to 2 keys
(current + previous) from a `cloud_stripe_secrets` table **or
config**". No migration creates such a table; no plan declares it.

**Recommendation.** plan-25-14 must own the table (or commit to the
config-only path and remove the table reference). Land a schema
alongside `cloud_webhook_events` in slot 0004:

```sql
CREATE TABLE cloud_stripe_secrets (
    id SERIAL PRIMARY KEY,
    secret_sealed BYTEA NOT NULL,
    active_from   TIMESTAMPTZ NOT NULL,
    retired_at    TIMESTAMPTZ
);
```

### 1.13 [MINOR] Hourly bandwidth tracking implied by 25.25, not declared by 25.11

[plan-25-25:130](epics/25-cloud-relay/plan-25-25-abuse-detection.md)
"hourly tracking is in Redis: extend the meter (25.11) to keep a
24-key rolling hour count (`bw:hourly:{sid}:{hour}`)." plan-25-11's
meter only writes daily keys (`bw:<server_id>:<YYYY-MM-DD>`). The
plan-25-25 abuse detector expects a meter shape that plan-25-11
doesn't ship.

**Recommendation.** Have plan-25-11 §2 own the hourly key shape.

### 1.14 [MINOR] `cloud_audit.is_admin` + `cloud_audit.reason` are added in slot 0006 but plan-25-02 already inserts admin audit rows

plan-25-20:21 ALTERs `cloud_audit` to add `is_admin` and `reason`.
That's only meaningful if `cloud_audit` exists with the simpler
shape before slot 0006. Per §1.5, the ownership conflict makes the
add-vs-create order non-obvious. plan-25-02 lacks `is_admin`, so
between slot 0002 and slot 0006 every admin-triggered audit row has
to be either re-classified or rewritten.

**Recommendation.** Fold the ALTER into plan-25-20's canonical
CREATE — i.e., make plan-25-20 the sole creator of `cloud_audit`,
include `is_admin` + `reason` from day one.

### 1.15 [MINOR] Bearer cache invalidation: 25.16's `registry.PurgeBearer` is called but never declared

[plan-25-16:69](epics/25-cloud-relay/plan-25-16-server-status-dashboard.md)
"admin path calls `registry.PurgeBearer(serverID)` so a stale cache
entry can't accept the old token." plan-25-08 §4 declares
`BearerCache` and a 5-min TTL, but the `PurgeBearer` API isn't
shown.

**Recommendation.** plan-25-08 must export `PurgeBearer(serverID)`
(or `PurgeByServerID(...)`) and assert at least one cross-pod path
in §8 tests.

### 1.16 [MINOR] Inter-pod registry RPC `POST /internal/registry/has` is unowned

plan-25-16:35 and plan-25-20:153 both reference cross-pod RPC
(`s.peers.ForceDisconnect`, `s.peers.IsOnline`) but neither plan
owns the RPC surface. The mTLS network claim is operational, not
in code.

**Recommendation.** Add a §0/§1 entry to plan-25-08 (registry owner)
that declares `cloud/internal/registry/peers.go` and the two
endpoints. Pin certificates / SPIFFE policy or document the
operator runbook.

---

## 2. Architecture §13 conformance

### 2.1 WSS frame format (architecture §13.4)

Canonical frame-type byte list (architecture.md:2661–2674):

```
0x01 REQ_HEAD       0x02 REQ_BODY      0x03 REQ_END
0x04 RESP_HEAD      0x05 RESP_BODY     0x06 RESP_END
0x10 PING           0x11 PONG
0x12 WINDOW_UPDATE
0x20 REVOKE         0x21 ENT_REFRESH
0x30 WS_HEAD
0x40 META_ENDPOINTS
```

[plan-25-07:50–64](epics/25-cloud-relay/plan-25-07-relay-tunnel-server.md)
and [plan-25-08:155–171](epics/25-cloud-relay/plan-25-08-relay-tunnel-cloud.md)
match the architecture byte-for-byte. **PASS.**

Frame payload encoding: architecture §13.4 doesn't pin a codec.
plan-25-07 §2 selects CBOR (`"CBOR for compactness + bounded
parsing"`). Plan-25-08 §2.2 also uses CBOR via
`cbor.Marshal(head)`. Internally consistent.

One detail to confirm: plan-25-08 §2.1 handles `FramePong` but
plan-25-08 §1 says "we don't initiate PINGs cloud-side in v1" —
the cloud only receives PINGs from the server. Architecture §13.4
says "PINGs every 25 s, deadline 10 s" without specifying the
direction. plan-25-07 §3.3 has the server initiating PINGs every 25s.
Consistent.

### 2.2 Role split (architecture §13.3)

Canonical role names: `api`, `relay`, `worker`.

- plan-25-01:11 (binary `maktaba-cloud` with `--role api|relay|worker`)
  matches.
- plan-25-08:9 (`--role=relay` binds `:8081`) matches.
- plan-25-09:9 (`--role=relay` adds HTTP/2 on `:443`) matches.
- plan-25-18:9 (APNs in `--role=worker`) matches.
- plan-25-19:9 (FCM in `--role=worker`) matches.
- plan-25-14:10 (Stripe webhook in api role + worker for async) —
  partial-fit; arch §13.3 says "webhook application" is a worker
  responsibility, so the api-role receipt path is an addition. Not
  a conflict, but document the api-vs-worker split explicitly.

### 2.3 Trust boundary (architecture §13.11)

- Stolen claim token, replay defenses: plan-25-06 §6 + §7 cover
  the architecture's per-IP rate limit and replay 409. **PASS.**
- Refresh-token replay: plan-25-02:268–282 implements cascade revoke
  + abuse event with kind `refresh_token_replay`. Matches arch
  §13.11 wording. **PASS.**
- Cross-user push: plan-25-17:136–139 matches `cross_user_push`
  abuse event + 403. **PASS.**
- Open proxy via relay: plan-25-09:117–123 handles
  `errReservedHost`/`errUnknownHost` and plan-25-25:42 has
  `relay_host_abuse`. **PASS.**
- Stripe webhook replay: plan-25-14 §2 `cloud_webhook_events.stripe_event_id
  PK` with `ON CONFLICT DO NOTHING`. **PASS.**

### 2.4 Subdomain validation (architecture §13.8)

Canonical regex: `^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$`.

- [plan-25-22:70](epics/25-cloud-relay/plan-25-22-subdomain-provisioning.md): `^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$` — **match.**
- plan-25-09:101: `subdomainRegex = ^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$`
  — **match.**

Reserved list: plan-25-22:41–49 includes the architecture's named
reservations (`admin`, `api`, `auth`, `billing`, `support`,
`maktaba`, `www`, `mail`). 90-day cooldown, 30-day grace — match.

### 2.5 Push: APNs + FCM (architecture §13.9)

Architecture: server posts `{user_id, kind, ref_id, data,
dedupe_key, ttl_seconds}` (architecture.md:2758–2762).

- plan-25-17:119–127: matches exactly (`dispatchReq`).
- Templates: arch §13.9 step 3 "Localizes copy via
  `cloud_push_templates(kind, locale)`" — plan-25-17 §1 creates the
  same table; seeds en + ar. **PASS.**

### 2.6 Distribution / installation (architecture §13.14)

- macOS (plan-25-27): DMG + brew cask + LaunchAgent + menu bar +
  Sparkle 2. **Match.**
- Windows (plan-25-28): EV-signed MSI + NSIS + Windows Service +
  tray + firewall + Recovery + per-user fallback. **Match.**
- Linux (plan-25-29): .deb + .rpm + Snap + Flatpak + AppImage; one
  systemd unit; `maktaba` user; `ProtectSystem=strict`. **Match.**
- Docker (plan-25-30): `ghcr.io/hamza-labs-core/maktaba` multi-arch;
  cosign-signed; compose reference. **Match** (though path issue,
  see §1.10).
- NAS, ARM, VPS, auto-update, wizard, uninstaller match arch §13.14
  bullets.

---

## 3. Cross-epic conflict matrix

| Cross-epic concern | Cited dependency | Status | Issue |
|--------------------|------------------|--------|-------|
| Server identity Ed25519 | Epic 10 Story 10.18 | **missing** | §1.2 |
| Local secret sealing | Epic 10.14 (plan-10-14) | exists | OK |
| mDNS source | Epic 15 Story 15.2 → should be 15.1 | wrong | §1.6 |
| License validation pattern | Epic 16 Story 16.4 (plan-16-04) | exists; **tier vocab diverges** | §1.4 |
| Audit log shape | Epic 21 Story 21.6 (slot 0054) | exists; **cloud shape diverges** | §1.5 |
| Rate-limit middleware approach | Epic 23 plan-23-06 | partial alignment; **see §1.4 of PLAN_REVIEW_18_24** | minor |
| Admin SSO ACR pattern | Epic 23 plan-23-01 | aligned | OK |
| Cloud-side audit ownership | Epic 21 plan-21-06 + Epic 25 README slot 0006 | **claimed by both** | §1.5 |
| Top-level Go module | Epic 22 plan-22-02 | **assumed but missing** | §1.10 |

### Local-vs-cloud schema divergences (acceptable, but worth noting)

The local `users` table uses `username` + `is_admin` + `failed_attempts` +
`locked_until` (slot 0029). The cloud's `cloud_users` uses `email`
(citext) + `plan` + `stripe_customer_id` + `display_name` + `locale` +
`timezone`. Different *databases*, different shapes; consistent with
architecture §13.6 (cloud is OAuth-2.1, local is bearer/signed-URLs).
**Not a conflict** — flagged for completeness.

Local audit_log (slot 0054) and cloud_audit (plan-25-02 / -20) differ
as noted in §1.5. **Real friction**, but addressable.

The cloud's notify channels are:

- `tier_changed` (plan-25-12, plan-25-14, plan-25-20)
- `subdomain_changed` (plan-25-09, plan-25-22)
- `tls_renewed` (plan-25-23)

None collide with local server's existing NOTIFY channels
(`jobs.progress`, `videos.new`, `settings_changed`, etc.). **PASS.**

---

## 4. Implementation feasibility (versions / import paths)

| Plan | Library / version | Resolved? |
|------|-------------------|-----------|
| plan-25-01 | `github.com/pressly/goose/v3` | OK — already in `api/go.mod` (v3.22.1) |
| plan-25-02 | `github.com/alexedwards/argon2id` | not in api/go.mod; new dep |
| plan-25-03 | `golang.org/x/oauth2`, `github.com/coreos/go-oidc/v3` | new deps |
| plan-25-04 | `github.com/golang-jwt/jwt` (implied by ES256 minter) | not pinned; **specify v5** |
| plan-25-05 | `github.com/davidbyttow/govips/v2` (libvips bindings); R2 client implied | govips is cgo-heavy; specify version + ensure CI has libvips-dev |
| plan-25-07 | `github.com/coder/websocket`, `cbor` library | cbor lib not specified — pin `github.com/fxamacker/cbor/v2` |
| plan-25-08 | `github.com/coder/websocket`, bcrypt, `github.com/hashicorp/golang-lru/v2` (implied) | LRU lib unspecified |
| plan-25-11 | `github.com/redis/go-redis/v9` (implied) | **never named explicitly**; pin to v9 |
| plan-25-12 | go-redis | same — pin |
| plan-25-13 | `github.com/stripe/stripe-go/v76` | OK, pinned |
| plan-25-14 | same | OK |
| plan-25-17 | go-redis + crypto/AES-GCM | pin go-redis |
| plan-25-18 | `github.com/sideshow/apns2` | pin version |
| plan-25-19 | `golang.org/x/oauth2/google` + `google.golang.org/api` | pin |
| plan-25-22 | citext + pg_trgm in PG | requires PG extensions installed; runbook |
| plan-25-23 | `github.com/go-acme/lego/v4` + Cloudflare DNS provider | pin |
| plan-25-24 | go-redis (Lua scripts) | pin |
| plan-25-26 | `github.com/cyberphone/json-canonicalization` (JCS), Ed25519 | pin |

**Recommendation.** plan-25-01 §9 currently says "None" for
Dependencies, but it owns `cloud/go.mod`. Land a `cloud/go.mod` in
plan-25-01 with **every** library pinned to a specific minor; the
remaining 35 plans then *use* (not introduce) those deps. Avoid the
22-epic pattern where dep versions drifted across plans.

### Redis dependency unspecified

**Five plans** rely on Redis as a hard dependency but no plan owns
the Redis deployment, version, or HA topology beyond a single line:

- plan-25-08:14 LRU is in-memory, no Redis
- plan-25-11:11 "Redis hash per (server_id, utc_date)"
- plan-25-12:121 `g.redis.HLen(...)`
- plan-25-17:148 rate-limit Redis key
- plan-25-24:9 "Sliding-window via Redis Lua"

`architecture.md:2614` says "Redis (sliding-window counters, rate
limits, dedup)" — canonical. None of plans 25.01/25.11/25.24 owns
the version pin, Sentinel HA setup, or persistence policy. plan-25-24
§9 says "Redis Sentinel HA in prod" and `≤5s loss` — operational only.

**Recommendation.** plan-25-24 (the first Redis-touching plan in
dependency order) should own the version (Redis 7.x), Sentinel
topology, and persistence (`appendonly yes` recommended for
rate-limit counters; `appendonly no` for soft caches). Cite in
plan-25-11, plan-25-12, plan-25-17.

### Object storage (R2)

plan-25-05 §0 introduces Cloudflare R2 (S3-compatible). No plan
declares the R2 binding library (`aws-sdk-go-v2` with R2 endpoint?
`minio-go`?). plan-25-01 §2 has no R2 config block.

**Recommendation.** plan-25-01 §2 must add an `[r2]` config block:
`endpoint`, `access_key_id`, `secret_access_key`, `bucket_avatars`,
`bucket_exports`. plan-25-05 §0 should pin the SDK.

---

## 5. Per-plan verdicts

### plan-25-01 — Cloud API service bootstrap

**Verdict:** NEEDS_REVISION

- `[major]` §1 migration filename convention is the root cause of
  §1.1's collisions; pin a less ambiguous format (`NNNN_<topic>.sql`
  matching `shared/db/migrations/MANIFEST.md`, or
  `NN-MM_<topic>.sql` to keep slot+seq visually distinct).
- `[major]` §9 Dependencies "None" omits Redis (used by 25.11, 25.12,
  25.17, 25.24) and R2 (used by 25.05). Add them here as the
  bootstrap deps so 35 follow-up plans can reference one source of
  truth.
- `[minor]` §2 `[admin] allowed_email_domain = "hamzalabs.com"`
  hardcodes the SSO domain. plan-25-20:54 also hardcodes
  `@hamzalabs.com`. Acceptable for v1; consider extraction.
- `[minor]` §6 graceful shutdown: arch §13.3 says "tunnel state is
  node-local; never replicated." plan-25-08 §7 also does graceful
  shutdown but with a different `shutdown_grace=5s` (vs plan-25-01's
  20s). Document or unify.
- `[minor]` §4 middleware order ends at `cors → routes`. Where does
  rate-limit (plan-25-24) slot in? Define here.

### plan-25-02 — Email + password registration

**Verdict:** BLOCKED

- `[blocking]` Migration filename collision; see §1.1.
- `[blocking]` Creates `cloud_audit` outside its README-assigned slot;
  see §1.5.
- `[major]` Creates `cloud_jwks` here but plan-25-26 creates a
  parallel `cloud_entitlement_keys` keystore; see §1.11.
- `[major]` §1 `cloud_users.tos_version_accepted TEXT NOT NULL`
  cannot be NOT NULL if account-create paths exist that don't pass
  it (OAuth-only register; plan-25-03 §4 Path B creates an OAuth
  user with no TOS field). Either drop NOT NULL, default to a
  sentinel, or have OAuth flows accept TOS at first sign-in.
- `[major]` §10 reset-password "Revoke all sessions" SQL only filters
  `WHERE user_id = ? AND revoked_at IS NULL`. Good. But the
  follow-up "send notification email" step is fire-and-forget; if
  the email worker is down it's silently dropped. Use outbox
  pattern.
- `[minor]` §3 `NormalizeEmail` strips trailing dot/whitespace via
  `mail.ParseAddress`. `mail.ParseAddress("user@host.")` accepts
  trailing dot in modern Go; consider explicit guard.
- `[minor]` §11.2 `TestRegisterEmailEnumeration` only asserts shape
  equality, not timing. Add a timing-budget assert (already in
  `TestTimingSafeLogin` for login, replicate for register).

### plan-25-03 — Google OAuth sign-in

**Verdict:** NEEDS_REVISION

- `[major]` Migration filename slot conflict; see §1.1.
- `[major]` §1 places `cloud_identities` in slot 0002, but README
  slot 0001 reserves identities. Re-file as `00010002_*`.
- `[minor]` §5 `validateNext` allowlist excludes `https://maktaba.app`
  (the marketing site) but [`README.md:144`](epics/25-cloud-relay/README.md)
  CORS list in plan-25-01:144 includes it. Reconcile.
- `[minor]` §3 `Provider.AuthURL` signature includes `nonce` and
  `codeChallenge` separately — but `nonce` is OIDC-specific (Google
  yes, GitHub no). Document the assumption.

### plan-25-04 — Apple OAuth

**Verdict:** NEEDS_REVISION

- `[major]` Migration filename slot mismatch (`00020003_*` if
  plan-25-03 stays in slot 0002, else `00010003_*`); see §1.1.
- `[minor]` §3 hardcodes 6-month JWT cap; Apple actually permits
  6 months max but iss=team_id rotation might trigger sooner. Cite
  Apple docs version.
- `[minor]` §5 `user` payload parsed via `appleUserPayload`; Apple
  docs warn the JSON shape can change. Add a versioned schema
  fallback.
- `[minor]` §7 `cloud_apple_notifications` `(event, sub, event_time_ms)`
  uniqueness mentioned in prose but not in the migration's unique
  index. Add the index.

### plan-25-05 — Account profile, deletion & data export

**Verdict:** NEEDS_REVISION

- `[major]` §1 migration filename `00090009_account_profile.sql` —
  inconsistent with the slot+seq convention used by every other
  plan (slot 0009, sequence 0001 would be `00090001_*`). Match
  convention; see §1.1.
- `[major]` §0 R2 binding library unspecified; see §4.
- `[major]` §9 libvips via `govips/v2` is cgo-heavy. CI build
  matrix must install `libvips-dev`. Plan-25-30 Dockerfile §1 already
  installs `libvips42` (good); plan-25-29 Linux packages must too.
- `[minor]` §6 purge map: `cloud_audit` UPDATE `actor_user_id = NULL`
  + `payload = jsonb_strip_path(payload, ['email','name'])`. But
  the local audit table (see §1.5) doesn't have `actor_user_id`;
  it has `actor_user`. This is the cloud's table — OK in isolation,
  but if §1.5 is fixed (single canonical audit shape), this purge
  must update accordingly.
- `[minor]` §8 export `signed_url_expires_at = now()+7d`. R2's
  presigned URL max is **7 days exactly** — be careful with edge.
  Cap at 7d − 1h for clock safety.

### plan-25-06 — Server claim token flow

**Verdict:** BLOCKED

- `[blocking]` Depends on Epic 10 Story 10.18 (Ed25519 server
  identity); see §1.2.
- `[blocking]` Migration filename collision with plan-25-02; see
  §1.1.
- `[major]` §5 token math contradicts story §13.7; see §1.9.
- `[major]` §1 `cloud_servers` adds `update_available` column? No —
  that column is added by plan-25-16 §6 implicitly (`UPDATE
  cloud_servers SET update_available = ...`) but no migration
  declares it. Land in plan-25-16's own migration; flag here as a
  cross-plan dep.
- `[minor]` §3 server-side `internal/cloudlink/claim.go` lives in
  the local-server repo per §1.3; once that's resolved, fine.
- `[minor]` §8 Dependencies list mentions "25.26 entitlement signing —
  needs to land first or be stubbed". Document the stub more
  carefully; plan-25-26 has its own keystore.

### plan-25-07 — WSS relay tunnel: server side

**Verdict:** BLOCKED

- `[blocking]` Module placement undeclared; see §1.3.
- `[blocking]` Depends on Epic 10 Story 10.18; see §1.2.
- `[major]` §2 CBOR codec unpinned; see §4.
- `[major]` §3.1 reads `ws.Read(ctx)` then `frame.DecodePayload(payload)`
  but earlier said `1 frame per WS message`. Confirm cap: WS msg
  size and frame size must match. `coder/websocket` default
  `MaxReadBytes` is 32MiB; plan §2 caps frame at 1 MiB. Set
  `Conn.SetReadLimit(1 << 20)`.
- `[minor]` §3.3 pinger uses `time.AfterFunc` for the 10s deadline;
  on rapid reconnects the AfterFunc may outlive its scope. Use a
  context-bound goroutine.
- `[minor]` §10 deps list "25.26 (entitlement signing — cloudlink
  consumes ENT_REFRESH frames)" — verifier code in §7 references
  `c.verifier.Verify` which is owned by 25.26; cross-link more
  explicitly.

### plan-25-08 — WSS relay tunnel: cloud side

**Verdict:** NEEDS_REVISION

- `[major]` §4 bcrypt path: `SELECT … FROM cloud_server_tokens …
  LIMIT 5000` then iterates running bcrypt per row. At 50ms/bcrypt
  × 5000 rows = 4 minutes per cold lookup. The cache mitigates, but
  cold-start cost is unacceptable. Use a token-prefix index or a
  faster digest (HMAC-SHA256 with a long salt) instead of bcrypt
  for *lookups*; reserve bcrypt for the rotation-resistance property.
- `[major]` §1 RelayServer binds `:8081`; plan-25-09 §1 binds the
  proxy on `:80`. Both run in `--role=relay`. Confirm both can
  coexist in the same binary (yes, different listeners); plan-25-09
  doesn't re-declare 25.8 routes.
- `[major]` `PurgeBearer` API not exported; see §1.15.
- `[minor]` §6 `tunnel_bytes_total{server_id}` is high-cardinality
  by design; plan-25-08:259 says "separate scrape" — OK, but call
  out that Prometheus cardinality limits apply; consider
  `tunnel_bytes_per_user` as a coarser alternative.
- `[minor]` §2.2 stream ID rollover handling is defensive; good.

### plan-25-09 — HTTP relay proxy

**Verdict:** NEEDS_REVISION

- `[major]` §3 `Proxy.serve` does an HTTPS redirect from `:80`
  (`r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https"
  → 301`). At Hetzner LB the relay should never *see* `:80` for
  user traffic; LB strips TLS and forwards plaintext over the
  private network. The redirect is a leftover; either remove or
  document the "private LB → public LB" topology.
- `[major]` §3 `isStream(r)` switches on path suffixes (`.ts`,
  `.m4s`); the local API serves HLS segments on a
  session-scoped path. Document the regex; cross-link to
  Epic 8 streaming spec.
- `[major]` §4 `MaxBytesReader` 5 GiB cap is set per-request; per
  the architecture §13.10 Pro = 100 GB/month, so a single 5 GiB
  upload would consume 5% of cap. Reasonable, but document.
- `[minor]` §10 deps list includes 25.11 + 25.12 as hooks; both
  ship as no-op stubs first. Confirm test fixture matches.

### plan-25-10 — Direct-connection probe & LAN fallback

**Verdict:** BLOCKED

- `[blocking]` Migration filename collision (`00020002_*` with
  plan-25-03); see §1.1.
- `[major]` Wrong Epic 15 story number; see §1.6.
- `[major]` §4 `endpoints` handler reads `srv.UserID` to enforce
  ownership but the user can claim multiple servers, and the
  multi-user-family case shares servers across payer+invitees.
  plan-25-12:88 lookups payer for member; plan-25-10 §4 doesn't.
  Audit: in v1 a family member cannot probe a payer's server's LAN
  IPs. Document the limitation.
- `[major]` §5 client probe runs in browser context with
  `mode: "cors"`. Local API CORS allowlist (Epic 7) must accept
  the cloud-app origin for `/api/health`. Add to plan-07 cross-link.
- `[minor]` §1 `cloud_server_endpoints.candidates_sealed` is AES-GCM
  sealed via `seal.Marshal(t.dataKey, …)`. The sealing helper hasn't
  been declared — cross-link to plan-25-05 §0 (which mentions
  "same data-key seal") and plan-25-01 (which should declare it).

### plan-25-11 — Bandwidth metering

**Verdict:** NEEDS_REVISION

- `[major]` README slot 0003 lists `cloud_bandwidth_daily` +
  `cloud_streams_active` only; plan-25-11 adds `cloud_bandwidth_monthly`
  (a third table). Update README slot 0003 inventory or move
  monthly to slot 0006.
- `[major]` §4 flush worker uses `HGetAll` then `HIncrBy -bin, -bout`.
  Concurrent meter increments between `HGetAll` and `HIncrBy` are
  *lost from the read view* (they survive in Redis), but the next
  tick picks them up. Test §8 `TestRedisHIncrByDeltaModel` asserts
  this; add a property-test for concurrency.
- `[major]` Hourly rollup keys are owned by plan-25-25; declare here;
  see §1.13.
- `[minor]` §6 RollupMonth uses `pg_advisory_xact_lock(8472613)`.
  plan-25-01 §5 uses `pg_advisory_lock(8472612)` (no xact). Lock-ID
  registry doc would help.
- `[minor]` §7 `cloud_bandwidth_daily(user_id, date DESC)` index +
  `SUM` query — cardinality of user × date over months → table
  bloat. Consider partitioning by year if growth becomes a problem;
  not a blocker for v1.

### plan-25-12 — Tier enforcement

**Verdict:** NEEDS_REVISION

- `[major]` Tier vocabulary divergence; see §1.4.
- `[major]` `cloud_family_members` column drift; see §1.8.
- `[major]` §3 `errSuspended` maps to 503 ("`server_suspended`") —
  but architecture §13.10 talks about per-user state; "server"
  status is something else. Reuse the right error key
  (`account_suspended` or `tier_suspended`).
- `[minor]` §2 LRU cache 60s TTL: a worst-case tier downgrade
  takes 60s to propagate. Architecture §13.10 says "60-s LRU tier
  cache invalidated by `LISTEN tier_changed`" — matches; OK.
- `[minor]` §3 `errCircuitBreaker` calls into 25.8 `Tunnel.CancelStream`,
  but plan-25-08 doesn't expose that method explicitly. Cross-link
  required.

### plan-25-13 — Stripe checkout

**Verdict:** NEEDS_REVISION

- `[major]` Tier vocabulary divergence (Pro/Family vs
  pro_monthly/pro_yearly/...); see §1.4.
- `[major]` `cloud_subscriptions.stripe_customer_id` is a duplicate
  copy of `cloud_users.stripe_customer_id`; see §1.7.
- `[major]` `cloud_family_members` schema drift with plan-25-12;
  see §1.8.
- `[minor]` §4 iOS UA guard returns 451; user-agent strings are easy
  to spoof — document this as a UX nudge, not a billing gate.
- `[minor]` §6 `subscription.MainItemID` is read for upgrade preview;
  it's not stored in `cloud_subscriptions` migration. Either store
  it or call Stripe to refresh.
- `[minor]` §11 deps lists "25.12" but should also list "25.14"
  for the inverse direction.

### plan-25-14 — Stripe webhook

**Verdict:** NEEDS_REVISION

- `[major]` `cloud_stripe_secrets` table referenced but never
  declared; see §1.12.
- `[major]` §2.3 `apply*` functions take `tx pgx.Tx` — but the wider
  function uses `s.db.BeginTx(ctx, ...)` so the txn is shared
  correctly. Confirm rollback paths in all branches.
- `[minor]` §3.1 out-of-order guard via `last_event_at`: in rare
  cases Stripe delivers events with the same `ev.Created` timestamp.
  Tie-break on `id` order? Document.
- `[minor]` Reconciliation cron (§0) "Nightly: list active subs,
  compare with Stripe, fix drift. Bounded to 1000 calls/min." Add
  test for backoff on Stripe 429.

### plan-25-15 — Plan comparison & subscription UI

**Verdict:** PASS

- `[minor]` Tier vocabulary divergence cascades into UI strings;
  see §1.4.
- `[minor]` §3 `Pricing` page uses `staleTime: 5*60*1000` for
  fetchPlans — but a Stripe price change would land via plan-25-13
  config + webhook, not via this fetch. Document.

### plan-25-16 — Server status dashboard

**Verdict:** NEEDS_REVISION

- `[major]` `cloud_servers.update_available` column not in any
  migration; see plan-25-06 §1 + plan-25-16 §6 cross-link.
- `[major]` `registry.PurgeBearer` referenced but not declared;
  see §1.15.
- `[major]` Inter-pod RPC `/internal/registry/has` referenced; see
  §1.16.
- `[major]` §6 `semver_lt` Postgres function: plan declares "added
  in `00060001_admin_revenue.sql` (or stub via CASE on lexicographic
  compare with caveats — better to use Go-side per-row update)."
  Stub-vs-real is undecided; pin one.
- `[minor]` §5 WS subprotocol `"maktaba.cloud.v1"`: declare in
  architecture §13.2.

### plan-25-17 — Push notification ingest

**Verdict:** NEEDS_REVISION

- `[major]` §5 outbox claim SQL references `expired_locks` view;
  view never declared. Inline the expiry predicate.
- `[major]` §1 `cloud_devices_unique_token_idx` is `(user_id, platform,
  token_sealed)` partial WHERE `revoked_at IS NULL`. But
  `token_sealed` is AES-GCM ciphertext with a *fresh nonce per
  encryption* — same plaintext produces different ciphertexts. The
  unique index will never collide; the "upsert via unique partial
  index" reactivation logic in §2 cannot work.

  **Fix:** add `token_hash BYTEA NOT NULL` column (SHA-256 of
  plaintext token) and put the unique index on `(user_id, platform,
  token_hash)`. The sealed token stays for transport security.

- `[major]` Rate-limit override table `cloud_rate_limit_overrides`
  referenced; lives in plan-25-24 (slot 0008) which is *after* slot
  0005. Either re-order slot or have plan-25-17 declare a no-op
  fallback.
- `[minor]` §1 template seeds only cover en + ar; story §3 says
  "english + arabic templates". OK; document the locale fallback
  chain (`fr` → `en` per `Templates.Render`).
- `[minor]` §4 placeholder substitute strips `\n` but not other
  control chars. Strip all `Cc` Unicode class for safety.

### plan-25-18 — APNs dispatcher

**Verdict:** PASS

- `[minor]` §2 `payload.Custom("apns-interruption-level", ...)` — that's
  an aps dictionary key, not a custom one. Use `payload.InterruptionLevel("time-sensitive")`
  if apns2 exposes it.
- `[minor]` §3 `backoffDelay` uses `rand.Intn` without seeding;
  Go 1.20+ auto-seeds, but pin Go version in §0.

### plan-25-19 — FCM dispatcher

**Verdict:** PASS

- `[minor]` §2 `decrypt(row.TokenSealed)` is a method on
  `FCMDispatcher` but the function is never declared. Confirm
  symbol-level review catches it.

### plan-25-20 — Admin fleet console

**Verdict:** NEEDS_REVISION

- `[blocking]` `cloud_audit` ownership conflict with plan-25-02;
  see §1.5.
- `[major]` §1 ALTER assumes columns can be added; if §1.5 resolves
  by making plan-25-20 the sole creator, the migration body is
  much larger.
- `[major]` §3 `r.With(adminSession, adminACL)` — middleware functions
  declared but not shown elsewhere. Cross-link to §2.
- `[minor]` §5 `RevokeAllUserSessions` is shared with plan-25-02;
  declare once (which plan owns it?).
- `[minor]` §10 admin self-abuse: type-the-count UI guard is UX,
  not code; the migration / API don't enforce it. Document as
  optional.

### plan-25-21 — Admin revenue dashboard

**Verdict:** NEEDS_REVISION

- `[major]` §1 migration `00060002_revenue.sql` shares slot 0006
  with plan-25-20; the README says slot 0006 = 25.20 only. Either
  re-file plan-25-21 as slot 0011+ (not in README range) or extend
  README to claim both stories share 0006.
- `[major]` §3 SnapshotRevenue MRR query divides `priceCents/12` for
  yearly plans, but tier vocabulary (§1.4) is unsettled — if tiers
  become `Pro + interval=yearly`, the query changes shape.
- `[minor]` §2 StripeSync iterates `subscription.List` with no
  cursor management; on >1000 subs the API will paginate.
  `stripe-go` exposes auto-pagination; document explicitly.
- `[minor]` §5 `costPerUser` exposes per-user cost — confirm RBAC
  allows export of this PII-adjacent dataset.

### plan-25-22 — Subdomain provisioning

**Verdict:** NEEDS_REVISION

- `[major]` §3 `claim` handler does `tx.Exec("DELETE FROM
  cloud_subdomains WHERE name=$1")` after grace expires. But
  `cloud_servers.subdomain` FK (plan-25-06 §1 declares `subdomain
  CITEXT` but no FK shown) might block via `ON DELETE`. Re-test;
  add explicit `NULL` for `cloud_servers.subdomain` on release.
- `[minor]` §1 seeds ~22 reservations; README claims "200-word
  reserved list". Either ship the 200 here or document the source
  file.
- `[minor]` §7 wildcard DNS setup is a one-time ops task; add a
  runbook reference.

### plan-25-23 — TLS at the edge

**Verdict:** NEEDS_REVISION

- `[major]` §1 migration `00070002_tls_certs.sql` shares slot 0007
  with plan-25-22 (slot 0007); README maps slot 0007 to 25.22
  (subdomains). Extend README mapping.
- `[major]` §5 cipher suites list lacks `TLS_AES_128_GCM_SHA256`
  / TLS 1.3 suites — they're implicit in Go (TLS 1.3 ciphers are
  fixed), but the comment in §5 says "TLS 1.2 AEAD-only + TLS 1.3";
  spell out that TLS 1.3 suite list comes from stdlib.
- `[minor]` §6 OCSP stapling: stapled response refresh 12h; document
  fallback when CA OCSP responder is down.

### plan-25-24 — Rate limiting & quota

**Verdict:** NEEDS_REVISION

- `[major]` Redis dependency unpinned; see §4.
- `[major]` §2 Lua script: `redis.call("INCR", KEYS[1])` after a
  read-then-decide path. Time-of-check / time-of-use gap — two
  concurrent requests pass the read both finding `sum < limit`,
  both INCR. Sliding-window approximation accepts this; document
  the ±20% tolerance per §9.
- `[major]` §3 `Policies` list omits `relay_streaming_qps_per_user`
  but Epic 25 architecture §13.10 has concurrent stream caps. Cross-link
  to plan-25-12 (which owns the cap, not 25.24).
- `[minor]` §7 Cloudflare IP allow-list refresh "daily" — short
  TTL is safer; document failure mode if the file is unavailable
  (fail open or fail closed?).

### plan-25-25 — Abuse detection & response

**Verdict:** NEEDS_REVISION

- `[major]` §5 `bandwidth_daily` only at daily granularity; hourly
  detector needs `bw:hourly:...` Redis keys that plan-25-11 doesn't
  declare; see §1.13.
- `[major]` §6 scoring query uses `EXTRACT(EPOCH FROM ...)`; for
  Postgres-only this is fine, but cross-check Epic 24's
  Postgres+SQLite parity rule — the cloud is Postgres-only, so OK.
- `[minor]` §3 path-shape sanitization regex is implicit ("/api/libraries/[id]/videos");
  pin a sanitizer that handles UUIDs, integers, and slugs without
  leaking content.

### plan-25-26 — Entitlement signing

**Verdict:** NEEDS_REVISION

- `[major]` Tier vocabulary divergence; see §1.4.
- `[major]` §1 `cloud_entitlement_keys` keystore conflicts with
  `cloud_jwks` keystore (plan-25-02); see §1.11.
- `[major]` §3 `Payload` has `KID` field but `Signer.Sign` sets it
  *before* JCS — so the signature covers `kid`. Good. But the
  verifier in plan-25-07 §7 (`c.verifier.Verify`) needs a JWKS
  fetcher that knows the cloud's pubkey set. Cross-link to plan-25-26
  §5 (JWKS endpoint) and document the polling interval.
- `[minor]` §6.3 daily cron `PushFreshEntitlements`: 5M servers ×
  24h refresh = 5M signing operations/day. Ed25519 is fast (>50k/s/core);
  not a problem, but stage rollout.
- `[minor]` §7 `FeatureEnabled` only checks `cloud_relay` /
  `cloud_push`. The local server gates *which* features on
  entitlement? Cross-link to plan-16-04.

### plan-25-27 — macOS installer

**Verdict:** PASS

- `[minor]` §1 bundle structure assumes binaries are pre-built; pin
  the build pipeline (Epic 22 plan-22-02 cross-link).
- `[minor]` §6 Sparkle appcast EdDSA signing: pin the signing key
  location and runbook.

### plan-25-28 — Windows installer

**Verdict:** PASS

- `[minor]` §0 EV-signed MSI requires a hardware HSM; document the
  CI signing setup.
- `[minor]` Service Recovery setup: confirm Windows Server vs
  Windows Pro behavior parity.

### plan-25-29 — Linux packages

**Verdict:** PASS

- `[minor]` §1 multi-format publishing (APT + DNF + Snap + Flatpak +
  AppImage) is operationally heavy; document priority order in case
  one fails.

### plan-25-30 — Official Docker image

**Verdict:** NEEDS_REVISION

- `[major]` Top-level `cmd/` paths assumed; see §1.10.
- `[major]` §1 Dockerfile FFmpeg from `johnvansickle.com` is a
  trusted-but-third-party source. Pin SHA-256 of the tarball
  download for reproducibility.
- `[minor]` `libvips42` installed but plan-25-05 uses `govips/v2`
  which needs `libvips-dev` headers at *build* time. Multi-stage
  Dockerfile should add `libvips-dev` to Stage 1 if avatar processing
  is cgo-bound; verify.

### plan-25-31 — NAS support

**Verdict:** PASS

- `[minor]` §1 Vendor UID/GID matrix should be tested against each
  NAS vendor's actual default; documented but not asserted.

### plan-25-32 — Raspberry Pi & ARM

**Verdict:** PASS

- `[minor]` §1 setup script detects SD vs SSD rootfs — POSIX `sh`
  for portability is good; ensure no bash-isms.

### plan-25-33 — One-click cloud-VPS deploy

**Verdict:** PASS

- `[minor]` §2 cloud-init is shared with DO + Hetzner; differences
  in cloud-init implementations (DigitalOcean's flavor) should be
  noted.

### plan-25-34 — Auto-update mechanism

**Verdict:** NEEDS_REVISION

- `[major]` §1 manifest published at `releases.maktaba.app/manifest.json`;
  no plan owns the host. plan-25-22 § reservations include
  `releases` — good. Add a plan-25-23 entry to cover its TLS posture
  (the wildcard already covers it).
- `[minor]` Signed manifest EdDSA: pin which signing key (the cloud's
  entitlement key? a separate release-signing key?). Recommend a
  separate key; track in plan-25-23 / runbook.

### plan-25-35 — First-run setup wizard

**Verdict:** NEEDS_REVISION

- `[major]` §10 deps include "Epic 17 (theme + i18n)". Epic 17 exists
  but verify plan-17 series owns the bilingual + theme primitives
  the wizard uses.
- `[minor]` State machine resumability: persistence shape not pinned;
  document.

### plan-25-36 — Cross-platform uninstaller

**Verdict:** PASS

- `[minor]` "Never touch library files" contract is good; add a
  test that asserts no `rm -rf` on user-set library roots.

---

## 6. Recommended remediation order

Five PRs in sequence would unblock the epic:

1. **Migration slot canonicalization** — single PR that re-files
   plan-25-02, plan-25-03, plan-25-04, plan-25-05, plan-25-10 to
   match the README's slot reservations; adds
   `cloud/migrations/MANIFEST.md` (Epic 22 pattern); resolves the
   `cloud_audit` ownership conflict between plan-25-02 and
   plan-25-20 (recommend: plan-25-20 owns CREATE). Fixes §1.1 +
   §1.5 + §1.14.

2. **Epic 10 Story 10.18 landing** — either net-new plan-10-18 +
   story-10-18 (Ed25519 long-term server identity), or absorb the
   keygen into plan-25-06 §3 with explicit ownership. Unblocks
   plan-25-06, plan-25-07, plan-15-07. Fixes §1.2.

3. **Tier vocabulary unification** — architecture §13.10 doc edit
   + plan-16-04 + plan-25-12 + plan-25-13 + plan-25-26 alignment.
   Recommend the `tier + interval` schema. Fixes §1.4.

4. **Top-level Go module decision** — sibling to PLAN_REVIEW_18_24
   §1.9. Pick "no top-level module; cloudlink lives under
   `api/cmd/maktaba-cloudlink/`" and update plan-25-07, plan-25-30,
   plan-25-27, plan-25-36 accordingly. Fixes §1.3 + §1.10.

5. **Cross-plan reference tightening** — fixes column drifts (§1.7,
   §1.8), declares the missing helpers (`PurgeBearer`, peers RPC,
   `cloud_stripe_secrets`, hourly bandwidth keys, `expired_locks`
   view, `cloud_servers.update_available` column), and pins
   library versions in `cloud/go.mod` (plan-25-01). Fixes §1.11–§1.16
   + §4.

---

## 7. Notes outside scope

- The local server `audit_log` (slot 0054) and the cloud `cloud_audit`
  (slot ??? per §1.5) being different shapes is a *real* developer-experience
  hit even though no SQL collision occurs. A future Epic 26 candidate:
  promote a `shared/audit/go/` module that both servers can use, so
  CSV exports, redaction policies, and category enums are
  authored once.

- Architecture §13.7 should be updated to acknowledge the 40-bit /
  8-char base32 reality (plan-25-06 §5 already documents the
  brute-force math). The "96-bit" wording in arch is misleading.

- Epic 15.2 ("Global discovery / cloud relay") and Epic 25.07–25.10
  overlap conceptually. Epic 15 README should note that 15.2 is
  v0/stub, superseded by Epic 25.

- Story 10.18 not existing is also a problem for plan-15-07 (federation),
  flagged before Epic 25 plans were written. Fixing it is a
  cross-epic win.
