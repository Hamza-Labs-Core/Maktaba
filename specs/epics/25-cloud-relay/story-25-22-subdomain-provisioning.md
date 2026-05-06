# Story 25.22 — Subdomain provisioning (`{username}.maktaba.app`)

> Epic 25 · Cloud relay · Phase 5 (operations)

## Description

Each linked server gets a public subdomain of `maktaba.app`. The
subdomain is the user's stable, friendly URL: `mahmoud.maktaba.app`.
The cloud manages name reservations, uniqueness, DNS propagation, and
release-with-grace.

Behavior:

- During the claim flow (25.6), the user picks a subdomain in the
  cloud UI:
  - Validation: `^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])?$` — 3 to 32
    chars, lowercase ASCII + digits + hyphen, can't start/end with
    a hyphen, no consecutive hyphens.
  - Not in `cloud_subdomain_reserved` (200 reserved words; see
    list below).
  - Not currently claimed (case-insensitive uniqueness via
    `citext`).
  - Per-user limit: at most 5 subdomains owned at a time.
- On success, insert `cloud_subdomains(name, user_id, server_id,
  claimed_at)`. If the user already has a server linked but no
  subdomain, this binds the server to the new subdomain.
- DNS provisioning is **wildcard-based**: a single
  `*.maktaba.app A 1.2.3.4` record points all subdomains at the
  relay LB. Per-subdomain DNS records are *not* needed for v1.
  This means subdomain resolution is instant (no DNS propagation
  delay) — we control routing inside the relay (25.9).
- TLS: a wildcard cert covers all subdomains (25.23).
- **Username changes.** Allowed once per 90 days. The old
  subdomain enters 30-day grace where it 301-redirects to the
  new one; after 30 days it's released back to the pool.
- **Release on unlink (25.16).** Subdomain enters 30-day grace
  (`released_at`), 301-redirects to a static "this Maktaba server
  is no longer available" page; after grace, returns to pool.
- **No reuse of `redirect_until` subdomains.** They cannot be
  reclaimed by another user during grace.

Reserved words list (excerpt; full list is config):

```
admin, api, app, apps, auth, billing, blog, business,
cdn, ceo, cfo, cloud, console, contact, dashboard,
dev, developer, docs, download, downloads, email,
faq, ftp, gateway, git, help, host, hosting, hr,
imap, info, jobs, login, m, mail, maktaba, marketing,
mobile, news, ns, ns1, ns2, oauth, pay, payments,
ping, pop, pop3, postmaster, press, privacy, ratings,
relay, release, releases, root, sales, security,
server, servers, setup, sftp, shop, sms, smtp, ssl,
staging, status, store, support, system, terms,
test, tunnel, www, web, webmail, www-int
```

Plus `hamzalabs`, `maktaba`, founder names, and a
profanity list (multi-language).

## Acceptance criteria

- **Given** a user picks `mahmoud`,
  **when** they submit it during claim,
  **then** it is validated, not reserved, not taken,
  inserted into `cloud_subdomains`, and from that moment
  resolves to the relay LB.
- **Given** a user picks `admin`,
  **when** they submit,
  **then** the response is `400 reserved`.
- **Given** a user picks `mahmoud` but it's already taken,
  **when** they submit,
  **then** the response is `409 taken`.
- **Given** a user changes their subdomain,
  **when** the change succeeds,
  **then** the old name has `released_at = now()`,
  `redirect_until = now() + 30d`, and visiting it 301s
  to the new name.
- **Given** the subdomain has been in `released_at` for 31
  days,
  **when** another user requests it,
  **then** the request succeeds; the row is replaced.
- **Given** a user picks `mahmoud--ali` (double hyphen),
  **when** validated,
  **then** rejected `400 invalid`.
- **Given** a user picks 33 characters,
  **when** validated,
  **then** rejected `400 too_long`.
- **Given** a user already owns 5 subdomains,
  **when** they try to claim a 6th,
  **then** the response is `409 limit_reached`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | each reserved word | validate | rejected |
| T02 | unit        | various invalid chars (`Mahmoud`, `_`, emoji) | validate | rejected |
| T03 | integration | claim same name twice | second | 409 |
| T04 | integration | release + 30 days + reclaim by other user | flow | accepted |
| T05 | integration | username change | observe | redirect chain works for 30 days |
| T06 | regression  | unicode confusables (`mаhmoud` Cyrillic а) | validate | rejected (ASCII only) |
| T07 | a11y        | error messages | inspect | screen-reader announces "this name is taken" |
| T08 | unit        | profanity list match | check `nazi` | rejected |
| T09 | regression  | concurrent claim of same name from 2 users | both POST | one succeeds, other 409 |
| T10 | integration | wildcard DNS resolution | dig random subdomain | resolves to LB IP |

## Edge cases

- **DNS caching at user's resolver.** Wildcard DNS means
  any subdomain we mint resolves immediately at the
  authoritative end. Recursive resolvers may cache `NXDOMAIN`
  for a stale name briefly, but since we use a wildcard,
  there's no NXDOMAIN to cache.
- **Cloudflare proxy mode.** We proxy `*.maktaba.app`
  through Cloudflare for the TLS+CDN benefits on
  non-streaming endpoints; streaming endpoints (HLS chunks)
  are proxied with caching disabled.
- **Subdomain takeover risk.** Standard mitigation:
  the wildcard CNAME is at the apex of `*.maktaba.app`;
  users cannot point an external CNAME at us, so no
  subdomain dangling occurs in the user's DNS.
- **Custom domains.** Out for v1. A user wanting
  `maktaba.alice.com` is deferred; would need ACME
  per-domain.
- **Internationalized domain names (IDN).** Out for v1;
  ASCII only. Document.
- **Search engines.** Subdomains are `noindex` by default
  (the relay sets `X-Robots-Tag: noindex, nofollow`).
- **Subdomain transferred to another user.** Out of v1; on
  unlink + re-claim by a different user, the subdomain
  flows through grace to free pool, where the new user
  can claim. No direct transfer.
- **Brand impersonation.** `maktaba`, `hamzalabs`,
  founder names reserved. Process for trademark complaints
  documented in 25.25.
- **DNS provider outage.** The wildcard A record is at
  Cloudflare; a CF outage breaks resolution. Document
  v2 risk: secondary NS at a different provider.

## Files / packages

- `cloud/internal/server/subdomain.go` — validation,
  claim, release, change.
- `cloud/internal/server/reserved_list.go` — reserved
  words.
- `cloud/internal/server/profanity.go` — multi-language
  filter (small list, accept some false negatives).
- `cloud/migrations/00070007_subdomains.sql`.

## Open questions

- **Username squatting.** A user grabs `apple` early.
  We document a trademark-complaint process and reserve
  the right to revoke. Defer the formal policy; v1 is
  permissive.
- **Custom domains via ACME.** Big surface; defer.
