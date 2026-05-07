# Story 10.13 — Permission model

v1 ships `is_admin` as the only role. Per-resource scope is implemented
via a single `Authz` interface so v2 can add fine-grained roles without
rewriting handlers.

**AC-1 — Resource-scope checks.**
- **Given** a handler,
- **When** it accesses a resource,
- **Then** it calls `authz.Can(ctx, "video.read", video_id)` or
  `authz.Can(ctx, "library.write", library_id)`. Default v1 policy:
  - `*.read` → any authenticated user **with the relevant `library_id`
    in their `library_acl` rows** (or any user when single-user mode is
    on),
  - `*.write` → admin only (or owner-of-the-resource for user-scoped
    resources like `playback_state` and `saved_searches`)
  - `library.*` → admin only

**AC-2 — Per-user scope on `playback_state`.**
- **Given** any user,
- **When** they access `/api/videos/{id}` detail,
- **Then** the response's `playback_state` is filtered to their own
  user_id; users cannot read each other's playback positions.

**AC-3 — Saved searches are per-user.**
- **Given** Epic 7 Story 7.9,
- **When** a non-admin lists or reads,
- **Then** they see only their own.

**AC-4 — Failure mode.**
- **Given** an authorization failure,
- **When** detected,
- **Then** the response is 403 problem+json `type: forbidden` with a
  generic message (don't leak whether the resource exists).

**AC-5 — Streaming JWT carries `lib[]` for offline checks.**
- **Given** the API is the source of truth for ACLs,
- **When** any signed URL is minted (Story 10.8) or any access JWT is
  issued (Story 10.3),
- **Then** the `lib[]` claim snapshots the user's accessible libraries
  at issue time. Streaming uses this offline (Story 10.7); the
  trade-off is up-to-15-min staleness on access-token revocation
  (Story 10.5 AC-2).

**Test cases:**
- Integration: non-admin POST `/api/libraries` → 403.
- Integration: user A reading user B's playback_state → filtered out.
- Integration: admin reading any resource → allowed.
- Integration: a `lib`-less JWT cannot stream any video (every signed
  URL is rejected by Streaming).

**Edge cases:**
- A non-admin opens a stream session for a video — allowed (every
  authenticated user with the `lib` access can watch); the per-user
  `playback_state` records it under their id. Cross-user resume sync
  is intra-user only.
- A user is downgraded from admin to viewer mid-session — their
  in-flight access tokens still carry `is_admin: true` until they
  expire (15 min). For instant revocation, force a logout-all (Story
  10.5 AC-3) plus a full key rotation (Story 10.6 AC-5) if the
  in-flight tokens must be killed immediately.
