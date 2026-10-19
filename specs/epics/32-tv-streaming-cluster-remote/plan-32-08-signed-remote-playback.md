# Plan 32.8 — Signed playback for remote viewers — implementation

> Implementation plan for [story-32-08](story-32-08-signed-remote-playback.md).
> Uses slot 0121 (`tv_remote_sessions`). Design: [design.md](design.md) §7.5, §7.6.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Tokens are minted on the home with the RS256 signing key, `sub` = the broadcast's video id. | No Cloudflare API call per viewer; tokens die with the broadcast. (Fallback per the spike: input id + cool-down.) |
| D2 | Default TTL 60 min, refreshed by the heartbeat after half its life. | Short enough to bound a leaked URL or a removed user; the web player swaps tokens seamlessly. |
| D3 | Presence is the heartbeat (60 s), not Cloudflare's viewer count. | Per user and device, immediate, and works with `hideLiveViewerCount`. |
| D4 | The local Maktaba session is the remote auth; the relay only carries it. | Epic 25 relay is a transport; users and ACLs are the home's (Epic 10). |
| D5 | Tune long-polls up to 20 s, then 202. | One round trip in the common case (push already live or quick start). |

## 1. Files

**Create**
- `api/internal/tvremote/tokens.go` — `Mint(videoUID, ttl, countries) (token string, exp time.Time)` with `crypto/rsa` and a small compact-JWS encoder (RS256 only; no new dependency — `api/go.mod` has no JWT library)
- `api/internal/tvremote/sessions.go` — create/beat/end/expire; limits
- `api/internal/handlers/tvremote/viewer.go` — `POST /api/tv/remote/tune`, `POST /api/tv/remote/sessions/{id}/heartbeat`, `DELETE /api/tv/remote/sessions/{id}`
- `api/internal/handlers/tvremote/keys.go` — `POST /api/tv/remote/signing-key/rotate` (admin)
- tests

**Modify**
- `api/internal/router` — mount; viewer routes need a session; no allowlist
- `api/internal/handlers/tv/tv.go` — `Info` gains `remote` (32.9 uses it)
- `cloud/internal/relay` (Epic 25 code) — `isStream` must not match `/api/tv/remote/*` (add a test asserting it)

## 2. Tune

```go
func (h *Viewer) Tune(w, r) {
  u := principal.From(r); req := decode(r)
  if !h.remote.Ready()             { problem(w, 503, "remote_unavailable", h.remote.Reason()) ; return }
  if !h.remote.ViewerAllowed(u)    { problem(w, 403, "remote_not_allowed") ; return }
  if !h.acl.CanReadChannel(u, req.ChannelID) { problem(w, 404, "no_such_channel"); return }
  if h.budget.Exhausted()          { problem(w, 402, "budget_exhausted"); return }
  s, err := h.sessions.Open(ctx, u, req.ChannelID, req.Device) // idempotent per (user, channel, device)
  switch { case errors.Is(err, ErrUserStreamsFull): 429 …; case errors.Is(err, ErrChannelsFull): 429 …;
           case errors.Is(err, ErrStorageGuard): 503 remote_storage_full … }
  p, ok := h.pushes.WaitLive(ctx, s.PushID, 20*time.Second)
  if !ok { writeJSON(w, 202, starting(s)); return }
  tok, exp := h.tokens.Mint(p.VideoUID, h.ttl(), h.countries())
  writeJSON(w, 200, tuned(s, h.hlsURL(tok), exp))
}
```

`hlsURL(tok) = "https://" + customerDomain + "/" + tok + "/manifest/video.m3u8"`;
`refresh_after = exp − ttl/10`.

## 3. Heartbeat and expiry

`Beat(session)`: owner check; `last_beat_at = now`; ACL re-check (403 ends the session); when
`now ≥ token_exp − ttl/2` or `push.video_uid ≠ session.video_uid` → mint and return. The
reconcile loop (32.7) ends sessions with `last_beat_at < now − 150 s`.

## 4. Key rotation

Create a new key at Cloudflare, store it as `cf_signing_key` (old kept as `cf_signing_key_prev`
for the call), switch minting, delete the old key at Cloudflare, drop `prev`. Every session's next
heartbeat sees `token_kid ≠ current` and re-mints.

## 5. Tests

Gate order (one case each), push reuse, idempotent 202, token claims verified with the public key,
refresh at half life, video change, expiry sweep, rotation, ACL loss mid-session, the relay
`isStream` test.

## 6. Rollout

With 32.9's web player. First users: admins (`tv_remote.viewers = admins`) for a week, then all.
