# Story 35.9 — Auth across machines

## Description

*As a user, I want to sign in once and use any machine of the cluster as the same Maktaba; as the
owner, I want signing out, a password change or disabling a user to take effect on every machine at
once, without any node ever reading the sessions table.*

Sessions stay in the home's `web_sessions`, resolved only by the home. A node asks the home through a
signed delegation call, caches the answer 30 s, and evicts it when the home pushes `auth.revoked`
over the cluster channel. Signing in on a node creates the session on the home for the node's host.
An optional cluster sign-in carries a session from the home to another machine with a single-use,
PKCE-bound code.

Design: [design.md](design.md) §10; README R2, decision "Sessions across machines".

## Acceptance criteria

- **AC1** The hub sends `keys {delegation: {id, key, not_after}}` after `welcome`; the key is stored
  sealed in `home_secrets` (`cluster.node.<id>.delegation`, current + previous, 10-minute overlap),
  rotated daily, deleted on revoke. The node keeps it in memory and in `node.json` (0600).
- **AC2** Every node → home request carries `X-Maktaba-Node`, `X-Maktaba-Node-Key`,
  `X-Maktaba-Node-Ts`, `X-Maktaba-Node-Sig` (HMAC-SHA256 over method, path+query, ts, body SHA-256,
  delegated-session hash); the home rejects skew > 60 s, unknown or revoked nodes, replays, and logs
  each rejection.
- **AC3** `POST /api/cluster/delegate/introspect` resolves a session id, bearer refresh or PAT to
  `{principal, csrf_sha256, expires_at, origin_node_id}` or 401; the node caches positives 30 s and
  negatives 5 s, keyed by `sha256(sid)`. RS256 access tokens are verified on the node with the home's
  JWKS (cached 5 min, refetched on unknown `kid`).
- **AC4** Every revocation path on the home (sign-out, password change, user disabled, admin revoke,
  expiry sweep) calls `cluster.NotifyRevoked`; `auth.revoked {session_ids? | user_id?}` evicts node
  caches within 1 s while the channel is up. With the home unreachable, the node answers from its
  copy of the home's database (Epic 37): reads, its `local` routes and sign-ins, marked
  `X-Maktaba-Home: unreachable`; a change answers 503 `home_unreachable`, and after 30 s
  (`delegate_unreachable_grace`) `home` routes are refused at once (but the session probe and the
  sign-in).
- **AC5** Login, logout, password reset and MFA routes on a node are proxied to the home with
  `X-Maktaba-Origin-Node`; the session row gets `origin_node_id`; `Set-Cookie` has no `Domain`, so
  the cookie is host-only for the node; cookie attributes are `httpsec.SecureCookie`'s; CSRF double
  submit works per host (mutating requests compare `sha256(X-CSRF-Token)` to `csrf_sha256`).
- **AC6** Cluster sign-in (design §10.5): node shows "Continue as …"; home `authorize` issues a code
  (32 bytes, stored hashed, 60 s, single use, bound to node and S256 challenge, relative return path);
  node `callback` checks state and `exchange`s with the verifier; the home creates a child session
  (`parent_session_id`). Signing out on the home ends its children; signing out on a node ends only
  that child.
- **AC7** The node runs the home's middleware chain (roles, `IsAdmin`, library ACLs, rate limits per
  node and user) on the resolved principal; the home resolves the forwarded session itself on every
  proxied request and never trusts a node's claim about a user.
- **AC8** Migration slot S2 (next free at implementation time): `web_sessions.origin_node_id`,
  `parent_session_id` (FK, cascade), `cluster_sso_codes`, `cluster_nodes.delegation_key_id`,
  `delegation_rotated_at`; revoked from `maktaba_worker`.

## Test cases

- **TC1** `TestDelegationSignatureVectors` (Go home and node share `shared/testdata/cluster/delegation_vectors.json`).
- **TC2** `TestDelegationRejectsSkewReplayRevoked`.
- **TC3** `TestIntrospectCacheTTLAndNegativeCache`, `TestRevokedEvictsWithinOneSecond` (fake hub).
- **TC4** `TestFailClosedAfterGrace`.
- **TC5** `TestLoginThroughNodeSetsHostOnlyCookie`, `TestCSRFPerHost`.
- **TC6** `TestSSOCodeSingleUseBoundToNodeAndPKCE`, `TestSSOReturnPathMustBeRelative`,
  `TestHomeSignOutRevokesChildren`.
- **TC7** `ClusterSignIn.test.tsx` (the "Continue as" prompt, en + ar).

## Edge cases

- **EC1** Key rotation while requests are in flight: the previous key verifies for 10 minutes.
- **EC2** Clock skew > 60 s on a node: signatures fail with `clock_skew`, shown on the Machines page.
- **EC3** User disabled while their child session is cached on an offline-channel node: bounded by
  the 30 s TTL.
- **EC4** A node revoked mid-session: its key is deleted, every proxied call and introspection fails
  at once; the node shows the removed page.

## Transport

On the cluster link (story 35.16) session checks, revocations, keys and the SSO exchange travel on the
machine's mTLS control channel, whose certificate authenticates the node; the delegation HMAC over HTTP
described here is the legacy path while `cluster.link.mode = prefer` and is refused in `require`.

## Dependencies

35.8 (machine agent, node mode), 35.10 (home proxy); Epic 10 sessions, CSRF, JWKS; Epic 31 channel v2.
