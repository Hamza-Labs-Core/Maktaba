# Story 15.3 — Server-to-server federation (optional)

Two Maktaba instances can opt to share libraries with each other. A
federated library appears as a second row in the picker and is browsable
read-only by default.

**Anchors:** [`architecture.md` §6](../../architecture.md). Depends on
[Story 15.7](story-15-07-federation-api.md) for the API surface and
crypto details (resolves [REVIEW §5.4](../../REVIEW.md)).

## AC

- Pairing: admin generates a federation token via Story 15.7; remote
  server enters the token; both instances exchange Ed25519 public keys
  + a signed agreement.
- The crypto/wire details (token format, channel binding, MITM
  resistance) are owned by [Story 15.7](story-15-07-federation-api.md);
  this story consumes that contract.
- Federation is asymmetric: A → B can read A's `Lectures`, B → A can
  read B's `Films`; permissions per-library.
- Federated browsing uses the same GraphQL schema, with a
  `federationOrigin` field on every `Video`.
- Federated streaming: bytes flow directly from the owning server's
  Streaming Service to the consuming client (the client holds two JWTs).
- Federation is off by default and never enabled silently.

## TC

- Pair instance A and B; B sees A's `Lectures` library read-only.
- Play a video from a federated library on B: stream comes from A.
- Revoke federation: B no longer sees A's library; in-flight sessions
  expire on next manifest refresh.

## EC

- A is offline: federated library on B shows a banner "Source server
  offline".
- A renames a library: B's reference is broken; surface an admin warning.
- Conflict resolution: same `content_hash` on both A and B → B prefers
  its local copy unless the user explicitly browses A's library.
  (Conflict semantics align with whichever uniqueness scope is settled
  in [REVIEW §1.1.a](../../REVIEW.md); if global uniqueness wins, the
  collision is impossible and B simply mounts A's row read-only.)
- Federation token leaked: revocation is immediate via
  `DELETE /api/federation/{partner_id}` (owned by Story 15.7).
