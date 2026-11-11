# Story 57.12 — Turning it on in existing homes

> Epic 57 · Phase 4 · Depends on: 57.01, 57.02, 57.04, 57.07 · Needed by: —

## Why

Every existing home has users, libraries and devices, and no ratings. If the update started
enforcing device signals at once, a child's iPad on an adult's account would suddenly show nothing,
because unrated content is hidden by default (README decision 7). If it never enforced them,
parents would not know the feature exists. The update has to change nothing for anyone until an
admin sets it up, while making it easy to set up.

## Description

### 1. What the update does

- Migrations 0220–0222 add columns and tables; **no user becomes restricted**, no library gets a
  default tier, no rule exists.
- The background `parental_recompute` fills `age_tier` from ratings that already exist (Plex,
  guide, plugin `content_rating`), by library, newest first, at low priority.
- `app_settings.parental.enabled` starts **false**. While false:
  - device signals are **recorded** (57.04) but `ac` is not minted, so nothing is hidden;
  - restricted policies cannot exist yet (none do).
- `parental.enabled` becomes true by itself the moment an admin makes the first account
  restricted, or sets the first library default tier. An admin can also turn it on in Family.
  It can be turned off only by an admin with no restricted accounts left.

### 2. Telling admins

When a signal arrives while `parental.enabled` is false and the device's limit is below 18, admins
see one notice (the notification centre, Epic 33; and a banner in the Player's Family row):
"A device signed in to Maktaba says a child uses it. Set up parental controls." It links to
Family > Rate libraries, then to Make an account restricted. Shown once per device, never repeated
after dismissal.

### 3. Rate libraries, pre-filled

Rate libraries (57.07 §3) pre-selects **All ages** for libraries whose content type
(`videos.content_type`, Epic 26) is mostly lectures or Quran recitation (≥ 90 % of classified
videos), and **Not rated** for the rest. Nothing is saved until the admin confirms.

### 4. Rollback

Turning `parental.enabled` off (no restricted accounts) stops minting `ac`. A downgrade to an older
release leaves the new tables unused. The `.down.sql` files drop them for the update rehearsal.

## Acceptance criteria

- **Given** an existing home after the update, **then** every user sees exactly what they saw
  before (the bypass rehearsal's unrestricted probes are unchanged).
- **Given** a child's iPad posting a 9–12 range before setup, **then** nothing is hidden and
  admins see the notice once.
- **Given** an admin rating the first library, **then** `parental.enabled` turns on and the iPad's
  limit applies from its next token refresh.
- **Given** a lectures library, **then** Rate libraries pre-selects All ages.
- **Given** the update rehearsal (CI), **then** up, down and restore pass with the new slots.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go + Postgres | a seeded pre-57 home snapshot | migrate; run probes as each user | identical answers to before |
| T02 | Go + Postgres | signal while disabled | POST; refresh | recorded; no `ac`; one notice |
| T03 | Go unit | first restricted account; first library tier | enable | `parental.enabled` true |
| T04 | Go unit | content-type mix 95 % lectures / 50 % | pre-fill | All ages / Not rated |
| T05 | update rehearsal (`tools/rehearsal`, CI only) | previous release → this | update, roll back, restore | passes |
| T06 | Player UI test (iPhone sim, ar) | `MockHome` notice | Family banner | mirrored; links to Rate libraries |

## Strings

| Key | English | Arabic |
|---|---|---|
| `parental.notice.title` | A child may be using Maktaba | ربما يستخدم طفلٌ Maktaba |
| `parental.notice.body` | A device signed in to Maktaba says a child uses it. Set up parental controls. | يشير جهاز مسجَّل الدخول إلى Maktaba إلى أن طفلًا يستخدمه. اضبط الرقابة الأبوية. |
| `parental.notice.action` | Set up | إعداد |
| `parental.enabled` | Parental controls | الرقابة الأبوية |
| `parental.enabled.offBlocked` | Lift every restriction before turning this off. | ارفع كل التقييدات قبل إيقاف هذا. |

## Edge cases

- **A home with many machines**: the setting lives in `app_settings` and copies like the rest;
  `parental_pins` does not copy (57.06).
- **SQLite desktop build** (single user): no restricted accounts can be useful there, but the
  tables exist so the schema stays the same.

## Build plan

| File | Change |
|---|---|
| `api/internal/parental/enable.go` (new), tests | the switch and its rules |
| `api/internal/notify/` | the notice |
| `api/internal/handlers/parental/signal.go` | no `ac` while disabled |
| `apps/apple/Player/Family/FamilyView.swift` | banner |
| `shared/db/migrations/0220–0222 .down.sql` | for the rehearsal |
