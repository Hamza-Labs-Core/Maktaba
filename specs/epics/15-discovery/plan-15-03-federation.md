# Implementation Plan — Story 15.3 Server-to-server federation (consumption surface)

> Companion to [story-15-03-federation.md](story-15-03-federation.md).
> The story states *what* and *why*; this plan states *how*.
> The pairing API + crypto live in
> [Story 15.7](story-15-07-federation-api.md). This story owns the
> read-side: the GraphQL federation field, ACL enforcement, federated
> streaming JWT minting, admin UI surface, and the conflict logic.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| GraphQL extension | `extend type Video { federationOrigin: FederationOrigin }` and a `federatedLibrary(partnerId, libraryId)` Query. Resolver in `api/internal/graphql/resolvers/federation.go`. |
| Streaming federation | Streaming Service (Epic 8) accepts a "federation JWT" minted by [Story 15.7](story-15-07-federation-api.md); this story wires the client to mint and present it. |
| ACL enforcement | Middleware `requireFederationScope(libraryID)` in `api/internal/auth/federation.go`. |
| Admin surface | Settings → Federation page in web/desktop (`web/src/features/settings/Federation.tsx`). |
| Out of scope | The crypto/token issue/exchange/revoke endpoints (Story 15.7). License/tier gate (Epic 16 Story 16.2). |

## 1. Architecture diagram

```
       ┌────────────────────────┐                  ┌────────────────────────┐
       │ Server B (consumer)    │                  │ Server A (owner)       │
       │  - federation_partners │                  │  - federation_partners │
       │  - federation JWT mint │                  │  - JWT verify          │
       └────────────────────────┘                  │  - streaming           │
                  │                                └────────────────────────┘
                  │ GraphQL: federatedLibrary(partner_id, library_id)
                  │  → fetch from A's GraphQL endpoint with federation JWT
                  │
                  ▼
       Client on B sees a row "Lectures @ A" mounted read-only
                  │
                  │ play
                  ▼
       Streaming flows directly A → client (B never proxies bytes)
```

## 2. Schema additions

`shared/graphql/schema.graphql`:

```graphql
type FederationOrigin {
    partnerId: UUID!
    displayName: String!
    libraryId: UUID!
    libraryName: String!
}

extend type Video {
    """When set, this video lives on a federated partner."""
    federationOrigin: FederationOrigin
}

extend type Query {
    """
    Browse a federated partner's library read-only. Requires that the
    caller's server has been paired with `partnerId` and the library
    is in the partner's ACL scope.
    """
    federatedLibrary(partnerId: UUID!, libraryId: UUID!): Library!
        @auth(scope: "federation:read")

    """List federated libraries the user can browse."""
    federatedLibraries: [FederatedLibrarySummary!]!
        @auth(scope: "federation:read")
}

type FederatedLibrarySummary {
    partnerId: UUID!
    partnerName: String!
    libraryId: UUID!
    libraryName: String!
    online: Boolean!
}
```

Resolvers fetch from the partner's GraphQL endpoint via a federation HTTP client that:

1. Looks up `partner.peer_origin_url` and `partner.peer_long_term_pubkey` from `federation_partners` ([Story 15.7](story-15-07-federation-api.md) schema).
2. Requests a federation JWT via `POST /api/federation/{partner_id}/token` (Story 15.7).
3. Signs requests with the JWT in `Authorization: Bearer ...`.
4. Validates the partner's GraphQL response was signed by the pinned long-term key (HTTP signature header `Signature`, RFC 9421).

## 3. ACL enforcement

The owner side (A) verifies on every federated request:

```go
// api/internal/auth/federation.go
func requireFederationScope(libraryID uuid.UUID) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            tok, err := jwt.ParseFromHeader(r, federationKeySet())
            if err != nil { http.Error(w, "401", 401); return }
            partnerID := uuid.MustParse(tok.Issuer())
            partner, err := db.GetFederationPartner(r.Context(), partnerID)
            if err != nil || partner.RevokedAt.Valid {
                http.Error(w, "401 partner-revoked", 401); return
            }
            if !aclAllowsLibrary(partner.ACL, libraryID) {
                http.Error(w, "403 library-not-in-scope", 403); return
            }
            ctx := context.WithValue(r.Context(), federationCtxKey{}, partner)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

## 4. Streaming with two JWTs

For a video on partner A streamed to a client on B, the client holds:

1. **B's session JWT** — issued by B (per Epic 10), proves the user is signed in.
2. **A's federation streaming JWT** — issued by A on demand via a federation token: B asks A for a streaming token scoped to a specific video; A signs with its streaming key; A's Streaming Service accepts.

Flow:

```
[Client on B] → [B GraphQL] {video.streamingURL?}
[B GraphQL]   → [A federation REST] /api/federation/{B's partner_id}/streaming-token?video_id=...
[A]           → [B] {hls_url, jwt_for_a, expires_at}
[Client]      → [A's HLS] with `Authorization: Bearer jwt_for_a`
```

The client never sees B in the data path. B's GraphQL resolver materializes A's `streaming_url` + `jwt_for_a` into the GraphQL response; the client uses those directly.

`api/internal/graphql/resolvers/federation_video.go`:

```go
func (r *Resolver) FederatedVideoStreamingURL(ctx context.Context, v *model.Video) (string, error) {
    if v.FederationOrigin == nil { return v.StreamingURL, nil }      // not federated, normal path
    fo := v.FederationOrigin
    tok, err := r.federation.MintStreamingTokenOn(ctx, fo.PartnerID, v.ID)
    if err != nil { return "", err }
    return fo.HLSURL + "?token=" + tok, nil
}
```

## 5. Asymmetric pairing

The story AC says federation can be one-way: "A → B can read A's `Lectures`, B → A can read B's `Films`". This falls out naturally from the per-partner ACL: A's `federation_partners` row for partner B has `acl = {libraries: [Lectures.id], read_only: true}`; B's row for A has `acl = {libraries: [Films.id]}`.

## 6. Conflict resolution

The story EC: "same `content_hash` on both A and B → B prefers its local copy unless the user explicitly browses A's library."

Implementation:

- The home page does not surface federated content automatically. A user must navigate into the federated library row (per AC: "browseable read-only by default" — they're separate rows).
- Search: limited to local content by default; an explicit toggle "include federated" extends to partners. When it does, results from the local server outrank results from partners with the same `content_hash`. Implemented as a join + `ORDER BY (federation_origin IS NULL) DESC` in the search aggregator.

If the global content-hash uniqueness scope (per [REVIEW §1.1.a](../../REVIEW.md)) ends up settled as global, then the collision is impossible at the schema level and B simply mounts A's library row read-only with no merge.

## 7. Revocation

When admin on A calls `DELETE /api/federation/{partner_id}` (Story 15.7), A's row gets `revoked_at = now()`. A's middleware refuses subsequent federation calls. Streaming JWTs already issued continue working until their `exp` (≤ 15 min); the EC accepts this.

A goroutine on B periodically (`60 s`) hits `GET /api/federation` on A's `peer_origin_url` to detect revocation; on detection, B clears its cached partner entry and surfaces "Source server federation revoked" inline. (Push notifications are an out-of-scope improvement.)

## 8. Admin surface (web/desktop)

`web/src/features/settings/Federation.tsx`:

- List of partners, with `online` indicator (last successful health-check < 60 s).
- "Pair a new instance" button → opens the SAS-comparison flow wired to [Story 15.7](story-15-07-federation-api.md) endpoints.
- Per-partner "Edit ACL" → calls `PATCH /api/federation/{partner_id}` with new `acl`.
- "Revoke" → calls `DELETE /api/federation/{partner_id}`.

A → B and B → A configurations are independent and shown side-by-side.

## 9. Test plan

### 9.1 Resolver

| Test | What it pins |
|---|---|
| `TestFederatedLibraryRequiresPartner` | Unknown `partner_id` → `404 partner-not-found`. |
| `TestFederatedLibraryRequiresACL` | Partner exists; library not in ACL → `403 library-not-in-scope`. |
| `TestFederatedLibraryReadOnly` | Mutations against federated content (mark watched, edit) → `403 federated-read-only`. |
| `TestVideoFederationOriginPopulated` | Video resolved through federation has `federationOrigin` populated. |

### 9.2 Streaming token

| Test | What it pins |
|---|---|
| `TestStreamingTokenScopedToVideo` | Token works for video X; replayed against video Y → `403`. |
| `TestStreamingTokenExpiry` | `exp` ≤ 15 min; expired token rejected by streaming. |
| `TestRevokedPartnerCannotMint` | After revoke, B's request to A's `/streaming-token` → `401 partner-revoked`. |

### 9.3 End-to-end

| Test | What it pins |
|---|---|
| `e2e_PairAndBrowse` | Pair A↔B (Story 15.7 flow); B sees A's `Lectures` row; opens; renders. |
| `e2e_FederatedPlayback` | Click play on a federated video; stream connects to A directly; bytes never traverse B. |
| `e2e_RevokeFlow` | Revoke from A's UI; B's row disappears within 60 s; in-flight session hits `exp` then fails. |
| `e2e_OfflineSourceShowsBanner` | Stop A; B's `federatedLibrary` returns degraded result with `online: false`. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| A is offline | B's `federatedLibrary` returns the cached library shape with `online: false` and a banner; `videos` array is empty. | `e2e_OfflineSourceShowsBanner` |
| A renames a library | B's reference (by id) still works; the surface name is fetched live. If the library is *deleted*, B's call returns `404 library-removed`; admin warning. | `TestFederatedLibraryRenamed` |
| Same content_hash on A and B | B prefers local; federated row visible only when user opens it explicitly; search results from local outrank federated. | `TestSearchPrefersLocal` |
| Federation token leaked | Revocation via `DELETE /api/federation/{partner_id}` (Story 15.7); B's middleware rejects within next request. | `e2e_RevokeFlow` |
| Streaming JWT survives revoke until `exp` | Documented trade-off (≤ 15 min); accepted per AC. | `TestStreamingTokenExpiry` |
| Network partition mid-stream | Streaming JWT is direct A → client; partition affects only that path. Failure is a normal HLS retry. | `e2e_FederatedPartition` |
| Admin on A modifies ACL mid-session | Cached partner ACL on A is invalidated by Postgres LISTEN; first request after change re-evaluates. | `TestACLChangeImmediate` |
| Conflict on `mdns_id` (two partners with same id) | Pairing rejected at Story 15.7's `/federation/pair`; this story sees only valid partners. | n/a |

## 11. Acceptance checklist

**Schema**
- [ ] `Video.federationOrigin` and `federatedLibrary(...)` shipped.

**Server**
- [ ] `requireFederationScope` enforces ACL; revoked partners refused.
- [ ] Streaming token mint/verify path works end-to-end.

**Client**
- [ ] Federated row appears in library picker; read-only mutations refused.
- [ ] Search results from local outrank federated.

**Admin**
- [ ] Settings → Federation lists partners with online state.

**Tests**
- [ ] All §9 tests pass on a 2-server docker-compose harness.

**Docs**
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.3.
