# Implementation Plan — Story 25.22 Subdomain provisioning

> Companion to [story-25-22-subdomain-provisioning.md](story-25-22-subdomain-provisioning.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoints | `POST /api/servers/{id}/subdomain`, `PATCH /api/servers/{id}/subdomain`, `DELETE …` (release immediately into grace), `GET /api/subdomains/check?name=...`. |
| Validation | Regex `^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$`, 3-32 chars, no double-hyphen, ASCII-only (defends against unicode confusables). |
| Reservations | Static list bundled at build (`reserved_list.go`) + DB table `reserved_slugs`. |
| Profanity | Multi-language small list bundled. Accept some false negatives. |
| Resolution | Wildcard `*.maktaba.app` DNS A record at Cloudflare; **no per-subdomain DNS calls**. The relay (25.9) does the routing. |
| Grace | 30-day 301-redirect after release; 90-day per-user change cooldown. |
| Out of scope | Custom domains (deferred). IDN (deferred). |

## 1. Migration `00080001_subdomains.sql` (slot 0008 per README)

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE subdomains (
    slug            CITEXT PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id       UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    provisioned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    cert_renewed_at TIMESTAMPTZ,
    released_at     TIMESTAMPTZ,
    redirect_until  TIMESTAMPTZ,
    redirect_to     CITEXT,
    last_changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX subdomains_user_idx ON subdomains(user_id);
CREATE INDEX subdomains_active_idx ON subdomains(slug) WHERE released_at IS NULL;

CREATE TABLE reserved_slugs (
    slug   CITEXT PRIMARY KEY,
    reason TEXT NOT NULL
);

-- Seed (truncated; full ~250 entries in seed file shipped with this story)
INSERT INTO reserved_slugs(slug, reason) VALUES
 ('admin','infrastructure'), ('api','infrastructure'), ('app','infrastructure'),
 ('auth','infrastructure'), ('billing','infrastructure'), ('cloud','infrastructure'),
 ('console','infrastructure'), ('contact','infrastructure'), ('dashboard','infrastructure'),
 ('dev','infrastructure'), ('docs','infrastructure'),
 ('hamzalabs','brand'), ('maktaba','brand'),
 ('mail','infrastructure'), ('relay','infrastructure'), ('releases','infrastructure'),
 ('staging','infrastructure'), ('status','infrastructure'), ('support','infrastructure'),
 ('test','infrastructure'), ('tunnel','infrastructure'), ('www','infrastructure');

CREATE OR REPLACE FUNCTION notify_subdomain_changed() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('subdomain_changed', NEW.slug::text);
  RETURN NEW;
END$$ LANGUAGE plpgsql;
CREATE TRIGGER subdomains_notify
    AFTER INSERT OR UPDATE ON subdomains
    FOR EACH ROW EXECUTE FUNCTION notify_subdomain_changed();

-- +goose Down
DROP TRIGGER IF EXISTS subdomains_notify ON subdomains;
DROP FUNCTION IF EXISTS notify_subdomain_changed();
DROP TABLE IF EXISTS reserved_slugs, subdomains;
```

## 2. Validation

```go
// cloud/internal/server/subdomain.go
var subRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$`)

func ValidateSubdomain(name string) error {
    if !asciiOnly(name) { return ErrInvalid }
    if !subRe.MatchString(name) { return ErrInvalid }
    if strings.Contains(name, "--") { return ErrInvalid }
    return nil
}

func asciiOnly(s string) bool {
    for _, r := range s {
        if r > unicode.MaxASCII { return false }
    }
    return true
}
```

Reservation check: combine `reserved_slugs` and an additional in-memory profanity list.

## 3. Claim

```go
// POST /api/servers/{id}/subdomain  body: {"name":"mahmoud"}
func claim(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sid, _ := uuid.Parse(chi.URLParam(r, "id"))
        var req struct{ Name string }
        decodeJSON(r, &req, 1<<10)
        name := strings.ToLower(strings.TrimSpace(req.Name))
        if err := ValidateSubdomain(name); err != nil { problem(w, 400, "invalid", ""); return }
        if s.isReserved(r.Context(), name) || s.isProfanity(name) {
            problem(w, 400, "reserved", ""); return
        }
        userID := currentUserID(r)
        // Per-user limit 5
        ownedCount, _ := s.repo.CountOwnedActive(r.Context(), userID)
        if ownedCount >= 5 { problem(w, 409, "limit_reached", ""); return }

        // Transactional insert: defeat race with FOR UPDATE on potential existing row
        tx, _ := s.db.BeginTx(r.Context(), pgx.TxOptions{})
        defer tx.Rollback(r.Context())
        var existing cloudSubdomainRow
        err := tx.QueryRow(r.Context(), `SELECT name, user_id, released_at, redirect_until FROM subdomains WHERE name=$1 FOR UPDATE`, name).Scan(&existing.Name, &existing.UserID, &existing.ReleasedAt, &existing.RedirectUntil)
        if err == nil {
            if existing.ReleasedAt == nil || (existing.RedirectUntil != nil && existing.RedirectUntil.After(time.Now())) {
                problem(w, 409, "taken", ""); return
            }
            // Past grace: replace
            _, _ = tx.Exec(r.Context(), `DELETE FROM subdomains WHERE name=$1`, name)
        }
        _, err = tx.Exec(r.Context(), `INSERT INTO subdomains(name, user_id, server_id) VALUES ($1,$2,$3)`, name, userID, sid)
        if err != nil { problem(w, 500, "internal", ""); return }
        _, _ = tx.Exec(r.Context(), `UPDATE servers SET subdomain=$1 WHERE id=$2`, name, sid)
        tx.Commit(r.Context())
        s.audit(r.Context(), "subdomain.claim", name)
        writeJSON(w, 200, map[string]string{"name": name})
    }
}
```

## 4. Change

```go
// PATCH /api/servers/{id}/subdomain  body: {"name":"new"}
func change(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sid, _ := uuid.Parse(chi.URLParam(r, "id"))
        userID := currentUserID(r)
        // 90-day cooldown
        last, _ := s.repo.LastChangedFor(r.Context(), sid)
        if time.Since(last) < 90*24*time.Hour {
            w.Header().Set("Retry-After", strconv.Itoa(int((90*24*time.Hour - time.Since(last)).Seconds())))
            problem(w, 429, "change_cooldown", ""); return
        }
        // Same validation + reservation + race-defeat as claim().
        // Inside txn: insert new row + update existing row with released_at=now() and redirect_to=newName.
        // …
    }
}
```

## 5. Release (on unlink, called by 25.16)

```go
func (r *Repo) ReleaseOnUnlink(ctx context.Context, serverID uuid.UUID) error {
    _, err := r.db.Exec(ctx, `
        UPDATE subdomains
        SET released_at = now(), redirect_until = now() + INTERVAL '30 days', redirect_to = NULL
        WHERE server_id = $1 AND released_at IS NULL`, serverID)
    return err
}
```

The 25.9 relay reads this state and 301-redirects (when `redirect_to` set) or serves the "no longer available" static page (when `redirect_to IS NULL` and `redirect_until > now()`).

## 6. Availability check

```go
// GET /api/subdomains/check?name=mahmoud
func check(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        name := strings.ToLower(r.URL.Query().Get("name"))
        if err := ValidateSubdomain(name); err != nil { writeJSON(w, 200, map[string]any{"available": false, "reason": "invalid"}); return }
        if s.isReserved(r.Context(), name) || s.isProfanity(name) {
            writeJSON(w, 200, map[string]any{"available": false, "reason": "reserved"}); return
        }
        taken, _ := s.repo.IsActiveOrInGrace(r.Context(), name)
        if taken { writeJSON(w, 200, map[string]any{"available": false, "reason": "taken"}); return }
        writeJSON(w, 200, map[string]any{"available": true})
    }
}
```

Rate-limit: 60/min per user (or 30/min per IP if anonymous).

## 7. DNS

One-time setup (not in code; runbook): create `*.maktaba.app A <lb-ip>` and `*.maktaba.app AAAA <lb-ipv6>` records via Cloudflare API. Wildcard means new subdomains resolve instantly without further DNS calls.

`X-Robots-Tag: noindex, nofollow` is set in the proxy (25.9) on all relay responses by default.

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestReservedRejected` | Each reserved word → 400. |
| `TestInvalidCharsRejected` | Uppercase, underscore, emoji → 400. |
| `TestUnicodeConfusables` | `mаhmoud` (Cyrillic а) → 400 (ASCII-only). |
| `TestDoubleHyphen` | `a--b` → 400. |
| `TestLength32Plus` | 33-char → 400. |
| `TestPerUserLimit5` | Sixth → 409. |

### 8.2 Integration

| Test | Pins |
|---|---|
| `TestClaimSameTwiceSecond409` | Race + retry. |
| `TestReleaseAfter30dReclaimByOther` | Past grace → accepted. |
| `TestChange90dCooldown` | Second change within 90d → 429. |
| `TestChangeRedirectChain` | Old 301 → new for 30d. |
| `TestProfanityListBlocks` | `nazi` → 400. |
| `TestWildcardDNSResolution` | `dig random.maktaba.app A` → LB IP (assumes runbook setup). |
| `TestConcurrentClaim` | Two POSTs same name → one 200, other 409. |
| `TestUnlinkReleasesIntoGrace` | After unlink, `redirect_until` 30d. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| DNS caching | Wildcard means no NXDOMAIN to cache. | Doc. |
| CF proxy mode for streaming | Streaming endpoints bypass CF cache; non-streaming proxied. | Doc. |
| Subdomain takeover | No external CNAME; wildcard at our zone. | Doc. |
| Custom domains | Out for v1. | Spec. |
| IDN | Out for v1; ASCII only. | Spec. |
| Search engine | `X-Robots-Tag: noindex`. | Proxy code. |
| Subdomain transfer | Via grace + reclaim by next user. | Spec. |
| Brand impersonation | Reserved-list + complaint process. | Doc. |
| CF outage | Wildcard cannot resolve; v2 secondary NS. | Doc. |
| Bulk reservation refresh | Migration drives reserved list; no UI. | Spec. |

## 10. Dependencies

- 25.1, 25.6 (`servers.subdomain`).
- 25.9 (host lookup invalidation on `subdomain_changed`).
- 25.16 (unlink calls `ReleaseOnUnlink`).

## 11. Acceptance checklist

- [ ] Migration 00080001 applies with seed reservations.
- [ ] Validator regex + ASCII-only + double-hyphen guard.
- [ ] Per-user 5 limit.
- [ ] 90-day change cooldown.
- [ ] 30-day grace + 301 redirect (cross-tested via 25.9).
- [ ] `GET /api/subdomains/check` rate-limited.
- [ ] `pg_notify subdomain_changed` triggers relay cache invalidation.
- [ ] Tests in §8 pass.
