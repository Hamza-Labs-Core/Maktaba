# Story 57.06 — Ask a parent, and the parent PIN

> Epic 57 · Phase 2 (API), 3 (iOS) · Depends on: 57.02, 57.05 · Needed by: 57.07, 57.08
> **Status:** built (checked against the code 2026-11-12): the API; asking and the PIN pad on iPhone, iPad, the web, the
> Android Player, Android TV and the desktop Player; answering a request on iPhone, iPad and
> the web (`/family/requests`).

## Why

A limit that is slightly wrong must be easy to fix in the moment: the child sees "This isn't
available" for something the parent would allow. Apple's Ask to Buy covers purchases and Ask for
More Time covers Screen Time, but nothing covers a single item in Maktaba's own library.
PermissionKit's questions are for communication and significant app updates, not content. So
Maktaba needs two ways: ask now and a guardian answers from their own phone, or a guardian who is
standing there types a PIN. The PIN must not be guessable, and it must not live on the child's
device.

## Description

### 1. Ask a parent

- `POST /api/me/parental/requests {subject_kind, subject_id}` by a restricted user (or a
  device-limited sign-in whose account has guardians). One pending request per subject per child;
  at most 20 pending per child (429 `too_many_requests`). Requests expire after 7 days.
- A request may also be for more time: `subject_kind` is `time` and `subject_id` is the minutes
  asked for, as text: a whole number from 1 to 480 written plainly (`"30"`). The apps offer 15,
  30 and 60. Approving it, or a correct PIN, makes a time grant for that day (57.14) and
  not an allow rule. A time request expires at the day line (04:00 home time).
- Guardians see them in Profile > Family > Requests (57.07): `GET /api/parental/requests`. Each
  shows the child, the title, poster and tier (the guardian may see them), and when it was asked.
- `POST /api/parental/requests/{id}/approve {expires_at?}` adds an allow rule (57.02) on that
  subject, with an expiry if chosen (Today, This week, Always), and answers `{request, rule}`.
  For a `time` request the body takes an optional `minutes` (1 to 480; left out, the minutes
  asked for) and no `expires_at`, and the answer is `{request, grant}`. `deny` closes it.
- The child's Player polls `GET /api/me/parental` on foreground, and through the websocket's
  `parental` event when connected. On approval the blocked page turns into the video page.
- **Notifications** (off by default, README §6.9): a guardian who turns them on gets a push
  through the notification hub (`api/internal/notify`), the device registry and Maktaba Cloud's
  push fanout, with only "A request is
  waiting" in the guardian's language. With them off, or with no cloud, the request waits on the
  home.

### 2. The parent PIN

- Each guardian may set a 6-digit PIN: `PUT /api/me/parental-pin {pin, password}` (the account
  password confirms it). Stored as argon2id in `parental_pins`, excluded from machine copies
  (`cluster_copy_excluded()`), never sent to any device.
- On the child's device: Enter parent PIN → choose the guardian (their display names) → 6 digits →
  `POST /api/me/parental/unlock {guardian_id, pin, subject_kind, subject_id}`. A correct PIN adds an
  allow rule on that subject for **today** (until 04:00 home time), answers `{rule}`, and writes
  an `approved` event naming the guardian. With kind `time` (the minutes in `subject_id`) it is a
  time grant instead: the answer is `{grant}`, a waiting request for time is answered, and a
  running stream learns of it at once. `GET /api/me/parental/guardians` lists who has a PIN to
  type; `GET /api/me/parental-pin` tells a guardian whether theirs is set.
- The PIN unlocks one item. It never changes the limit, never leaves the child's account, and
  never signs anyone in.

### 3. Limits on tries

| Counter | Limit | Then |
|---|---|---|
| Per guardian PIN (any device) | 5 wrong | locked 15 min; each further lock doubles, to 24 h; reset by a correct PIN or by the guardian changing it |
| Per child account | 10 wrong in 1 h (across guardians) | unlock refused for the child for 1 h |
| Per sign-in, per source address (and per relay connection) | 20 wrong in 1 h | 429 for 1 h |

Each lock writes a `pin_locked` event and, if notifications are on, a push ("Too many wrong PIN
tries"). Answers take a fixed minimum time (250 ms) so timing does not tell right from wrong
digits. Wrong and locked answers do not say which guardian's PIN was close.

## Acceptance criteria

- **Given** a child asking for a 13+ video, **when** the guardian approves "Today" from their
  iPhone, **then** the child's Player shows the video without a restart, and it is hidden again
  the next day.
- **Given** five wrong PINs, **then** the sixth try answers `locked` for 15 minutes, the guardians
  see `pin_locked` in Activity, and the child's device holds nothing that can check a PIN.
- **Given** a sixth lock in a row, **then** the lock is 24 h at most.
- **Given** a viewer, another guardian or the child, **when** they approve a request, **then** 403
  (child, viewer) or 404 (another guardian).
- **Given** notifications off, **then** no push is sent and the request appears when the guardian
  opens Family.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go + Postgres | request, approve with expiry | effective rules | allowed until expiry |
| T02 | Go unit | duplicate and 21st request | POST | one row; 429 |
| T03 | Go + Postgres | correct PIN | unlock | allow until 04:00; event |
| T04 | Go unit (fake clock) | wrong PINs 5, 10, 20 | unlock | the three locks; doubling to 24 h |
| T05 | Go unit | right vs wrong timing | unlock ×100 | both ≥ 250 ms, same codes shape |
| T06 | Go role test | six principals | request, approve, deny, PIN routes | 57.02's table |
| T07 | Go unit | notifications on/off | request | push payload has no title or name; none when off |
| T08 | Player UI test (iPhone sim, en) | blocked → Ask → approve via `MockHome` | wait | video page |
| T09 | Player UI test (iPad sim, ar) | PIN wrong ×5 | enter | locked message, mirrored keypad order kept left-to-right |

## Strings

| Key | English | Arabic |
|---|---|---|
| `parental.ask.sent` | Request sent. You'll see it here when a parent answers. | أُرسل الطلب. ستراه هنا عندما يرد أحد الوالدين. |
| `parental.ask.pending` | Waiting for a parent | بانتظار أحد الوالدين |
| `parental.ask.denied` | A parent said no. | رفض أحد الوالدين. |
| `parental.ask.tooMany` | You have too many requests waiting. | لديك طلبات كثيرة قيد الانتظار. |
| `parental.pin.choose` | Who is entering the PIN? | من يُدخل الرمز؟ |
| `parental.pin.enter` | Enter the 6-digit parent PIN | أدخل رمز الوالدين المكوّن من 6 أرقام |
| `parental.pin.wrong` | That PIN isn't right. | هذا الرمز غير صحيح. |
| `parental.pin.locked` | Too many tries. Try again in %@. | محاولات كثيرة. أعد المحاولة بعد %@. |
| `parental.pin.unlocked` | Unlocked for today. | فُتح لليوم. |
| `parental.push.request` | A request is waiting | يوجد طلب بانتظارك |
| `parental.push.locked` | Too many wrong PIN tries | محاولات خاطئة كثيرة لإدخال الرمز |

## Edge cases

- **The item is deleted before approval**: the request expires; approve answers 410.
- **The child's limit rises above the item while pending**: the request closes as `approved`
  automatically (no rule needed).
- **A guardian with no PIN**: not listed in the PIN chooser; if none have one, Enter parent PIN is
  hidden.
- **Keypad in Arabic**: the digits keep their phone order (1–9 left to right, as iOS does in
  Arabic); the field is left-to-right.

## Build plan

| File | Change |
|---|---|
| `shared/db/migrations/0222_parental_requests{,.sqlite}.sql`, `MANIFEST.md` | tables |
| `api/internal/handlers/parental/requests.go`, `pin.go` (new), tests | routes, limits |
| `api/internal/notify/` (the notification hub, Epic 33) | kind `parental_request`, delivered to the guardians' notification centre, and to mobile push (slot 0065 categories) only when notifications are on; title-free payload |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/AskParent.swift`, `ParentPIN.swift` (new) | flows |
| `apps/apple/Player/Profile/…xcstrings`, `MaktabaKit/Localizable.xcstrings` | strings |
