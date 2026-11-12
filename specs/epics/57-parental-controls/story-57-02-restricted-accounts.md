# Story 57.02 — Restricted accounts and guardians

> Epic 57 · Phase 1 · Depends on: — · Needed by: 57.03, 57.04, 57.06, 57.07, 57.08, 57.12, 57.13
> **Status:** built (checked against the code 2026-11-12). The role table of §2 is held by
> `api/internal/router/p57_roles_pg_test.go` (57.13).

## Why

The home has two roles, admin and viewer, and per-library read. A parent needs a third shape: a
viewer whose content is limited, who cannot change that limit, and whose limit can be managed by
the people looking after them, who are not always home admins. Every rule must be enforced by
the API and proven by role tests; hiding a button stops nothing.

## Description

### 1. The model

- A **restricted account** is a viewer with a row in `parental_policies` (README §6.5):
  `max_tier` (−1, 0, 9, 13, 16, 18), `unrated` (`hide`, or a tier to treat unrated content as),
  `allow_only`, `version`. A check refuses a policy on an admin, and refuses making a restricted
  user an admin (`PUT /api/users/{id}` answers 409 `restricted_admin`).
- A **guardian** of a child is an admin, or a user an admin named in `parental_guardians`. A
  guardian cannot be restricted, and a restricted user cannot be a guardian (409
  `restricted_guardian`).
- **Rules** (`parental_rules`): allow or block a library, collection, series, channel, YouTube
  source, plugin or video, optionally until `expires_at`. Block wins over allow; allow wins over
  the tier and the unrated rule. `allow_only` hides everything not allowed.
- Library ACLs still apply first: a rule cannot open a library the child has no ACL for. An allow
  on a library the child cannot read is refused with 422 `no_library_access`.

### 2. Who may do what

| Action | No session | Viewer | Restricted child (self) | Guardian of the child | Another guardian | Admin |
|---|---|---|---|---|---|---|
| Read the child's policy, rules, activity | 401 | 404 | own effective limit only (`/api/me/parental`) | 200 | 404 | 200 |
| Make an account restricted, change limit, unrated, allow-only | 401 | 403 | 403 | 200 (on their child) | 404 | 200 |
| Lift the restriction entirely (`DELETE`) | 401 | 403 | 403 | 403 | 404 | 200 |
| Add or remove rules | 401 | 403 | 403 | 200 | 404 | 200 |
| Name or remove a guardian | 401 | 403 | 403 | 403 | 403 | 200 |
| Create a new account as restricted | 401 | 403 | 403 | 403 (asks an admin) | 403 | 200 |

"Another guardian" means a guardian of a different child; the child's existence is not revealed,
so the answer is 404.

### 3. Routes

README §6.6: `GET /api/parental/children`, `GET|PUT|DELETE /api/users/{id}/parental` (GET returns the policy, guardians and each sign-in's signal; PUT carries `version`, 409 when stale),
`POST|DELETE /api/users/{id}/parental/guardians`, `GET|POST|DELETE /api/users/{id}/parental/rules`.
`POST /api/users` (admin) gains an optional `parental: {max_tier, unrated, allow_only, guardians}`
so an account can be made restricted from the start. Each change bumps `version`, writes the
audit log (`audit_log`, actor and before/after, no titles), and sends
`pg_notify('parental_policy', user_id)`.

### 4. Signing a child's device in

A guardian sets up the child's iPad by approving its sign-in code **as the child**:
`POST /api/pairing/approve` gains `as_user`. It is allowed only when the caller is the child's
guardian (or an admin), and the child must be restricted. A restricted user may approve a code
only for themselves, which gives a restricted sign-in, so a child cannot use the code flow to
bring an adult's account onto a device. The child's password still works as for any user.

### 5. Migration (slot 0221; see README §6.5)

The policy, guardian and rule tables; `session_age_signals` lands in the same slot for 57.04.

## Acceptance criteria

- **Given** an admin, **when** they make viewer Sara restricted at 9+ and name viewer Huda her
  guardian, **then** Huda can change Sara's limit to 13+ and add rules, and cannot lift the
  restriction or name another guardian.
- **Given** Sara, **when** she calls any write route on her own policy, **then** 403; **when** she
  reads `/api/users/{her id}/parental`, **then** 404, and `/api/me/parental` gives her limit only.
- **Given** a guardian of another child, **when** they read Sara's policy, **then** 404.
- **Given** a restricted user, **when** an admin tries to make them an admin, **then** 409.
- **Given** Huda approving a code sign-in `as_user: Sara`, **then** the device is signed in as Sara;
  **given** Sara approving a code, **then** the device is Sara's, restricted.
- **Given** a policy change, **then** the next request of Sara's (with a token minted before the
  change) sees the new limit.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit + Postgres | constraints | policy on admin; admin on restricted; restricted guardian | 409 each |
| T02 | Go role test | the six principals of §2 | every route of §3 | the table of §2 |
| T03 | Go unit | block and allow on the same video; allow on a 16+ video under 9+; `allow_only` | effective rules | block wins; allowed; only allowed |
| T04 | Go unit | allow on a library without ACL | POST rule | 422 `no_library_access` |
| T05 | Go + Postgres | token minted, then policy tightened | next request | new limit (cache invalidated by notify) |
| T06 | Go unit | `as_user` by guardian, by a stranger, by the child for an adult | approve | signed in as child; 404; 403 |
| T07 | Go unit | each change | audit | one row, no titles |
| T08 | migration test | up/down Postgres and SQLite | migrate | twins apply; MANIFEST row |

## Strings

The API returns codes. The Player shows (57.07):

| Key | English | Arabic |
|---|---|---|
| `parental.error.restrictedAdmin` | A restricted account can't be an admin. | لا يمكن أن يكون الحساب المقيَّد مشرفًا. |
| `parental.error.restrictedGuardian` | A restricted account can't look after another account. | لا يمكن للحساب المقيَّد أن يكون وليًّا لحساب آخر. |
| `parental.error.noLibraryAccess` | This account can't open that library, so it can't be allowed. | لا يستطيع هذا الحساب فتح تلك المكتبة، لذا لا يمكن السماح بها. |
| `parental.error.adminOnly` | Only an admin can do this. | لا يقوم بهذا إلا المشرف. |

## Edge cases

- **The last guardian is removed**: admins remain guardians of every child, so a child is never
  unmanaged.
- **A guardian's account is deleted**: their `parental_guardians` rows and PIN cascade away; their
  rules stay, with `created_by` set to null.
- **A child turns 18**: nothing changes on its own. An admin lifts the restriction (Q8).
- **Cloud-linked sign-in as a guardian** (play-scoped): can read the children's policy but not
  write it, as manage needs a `manage` link (the existing `cloudlink` rule).

## Build plan

| File | Change |
|---|---|
| `shared/db/migrations/0221_parental_policies{,.sqlite}.sql`, `MANIFEST.md` | tables |
| `api/internal/parental/policy.go`, `cache.go` (new) | load, cache, notify |
| `api/internal/handlers/parental/accounts.go` (new), `accounts_pg_test.go` | routes, T02 |
| `api/internal/handlers/users/` | `parental` on create; 409 on admin |
| `api/internal/handlers/discovery/device.go` | `as_user` |
| `api/internal/router/` | mount |
