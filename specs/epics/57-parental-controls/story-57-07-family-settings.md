# Story 57.07 — Family settings for guardians in the Player

> Epic 57 · Phase 3 · Depends on: 57.02, 57.05, 57.06 · Needed by: 57.08, 57.12
> **Status:** built (checked against the code 2026-11-12) on iPhone and iPad, and on the web under `/family`.

## Why

Parents look after children from their own phone. The Remote app is for admins running the
home, while a guardian may be a viewer. So the Player on iPhone and iPad gets a Family section,
shown only to admins and guardians (`parental.can_manage` from capabilities). As built, the web
has the same settings under `/family` (§5), so a home with no iPhone can run parental controls.
The home still enforces every rule; both only call the routes of 57.02 and 57.06.

## Description

### 1. Where

Profile > **Family** (iPhone: a row that pushes a list; iPad: a split view with children on the
leading side). Hidden when `can_manage` is false. A guardian who is not an admin sees only their
children; an admin sees every restricted account and a **Make an account restricted** button.

### 2. The screens

| Screen | Contents | Routes |
|---|---|---|
| Family | children (name, limit, pending requests badge); Requests; Parent PIN; Notifications | `GET /api/parental/children`, `GET /api/parental/requests` |
| A child | Content limit (All ages, 9+, 13+, 16+, 18+, Nothing unless allowed); Unrated content (Hide, or treat as a tier); Only allowed items; Allowed; Blocked; Devices; Activity (57.08); Guardians (admin only); Lift restriction (admin only, with a confirm) | `PUT /api/users/{id}/parental`, rules routes, guardians routes |
| Allowed / Blocked | list of subjects with kind icons and expiry; Add: search the home's libraries, collections, series, channels, YouTube sources, plugins and videos (the guardian's own view), pick one | rules routes |
| Devices | each sign-in of the child, and each signed-in device carrying a signal: the device's name, its limit, its source (Age range, Screen Time), and a pending looser limit with **Approve** | `GET /api/users/{id}/parental` (signals), `…/signals/{family}/clear` |
| Requests | pending requests across children: title, poster, tier, child, when; Approve (Today, This week, Always), Deny | 57.06 routes |
| Parent PIN | set or change (asks the account password); shows whether locked | `PUT /api/me/parental-pin` |
| Notifications | off by default; on registers this device's push categories `parental_request`, `parental_pin_locked` | the device registry |
| Make an account restricted (admin) | pick a viewer, a limit, guardians; or create a new account restricted | `PUT /api/users/{id}/parental`, `POST /api/users` |

### 3. Rating libraries quickly

The child page and the first-run notice (57.12) link to **Rate libraries** (admins): each library
with its default tier picker (All ages, 9+, 13+, 16+, 18+, Not rated) and its unrated count.
`PUT /api/libraries/{id}/age-tier` (57.01).

### 4. Arabic

Every screen mirrored; lists of names sorted with Arabic collation when the app is in Arabic;
tier pickers in ascending age order in both languages.

### 5. On the web

The web app has the same settings for admins and guardians, in English and Arabic:

| Page | Contents |
|---|---|
| `/family` | the children; for an admin, making an account restricted, the notice and the switch of 57.12 |
| `/family/{id}` | a child: the limit, the unrated rule, only allowed items, Allowed and Blocked, Devices (signals; approve a looser one), guardians and Lift restriction (admin) |
| `/family/{id}/activity` | activity (57.08) |
| `/family/{id}/time` | time rules, today's use, more time (57.14, 57.15) |
| `/family/requests` | the requests that wait: approve or deny |
| `/family/pin` | the parent PIN |

Android (phone, tablet, TV) and the desktop Player have no guardian screens.

## Acceptance criteria

- **Given** a guardian who is a viewer, **then** Family lists only their children, and has no
  Guardians, Lift restriction or Make restricted controls.
- **Given** the guardian sets 13+ and adds an allowed collection, **then** the child's Player shows
  the change on its next foreground (57.05).
- **Given** a device with a pending looser limit, **when** the guardian taps Approve, **then** the
  device's limit becomes the looser one.
- **Given** an admin, **then** Rate libraries sets each library's default tier, and the unrated
  count goes down.
- **Given** a viewer with no children, **then** no Family row (and the routes answer 403 anyway).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest | capabilities `can_manage` true/false | Profile model | Family row shown/hidden |
| T02 | MaktabaKit XCTest | guardian vs admin | Family model | controls per role |
| T03 | Player UI test (iPhone sim, en) | `MockHome` guardian | set limit, add allowed, approve request | requests sent; lists update |
| T04 | Player UI test (iPad sim, ar) | admin | Rate libraries; Make restricted | mirrored; Arabic; requests sent |
| T05 | Player UI test (iPhone sim) | pending looser signal | Devices > Approve | clear route called |
| T06 | catalog check | `--stringsdata` | build | every key has Arabic |

## Strings

| Key | English | Arabic |
|---|---|---|
| `family.title` | Family | العائلة |
| `family.children` | Children | الأطفال |
| `family.requests` | Requests | الطلبات |
| `family.pin` | Parent PIN | رمز الوالدين |
| `family.pin.set` | Set a PIN | تعيين رمز |
| `family.pin.change` | Change PIN | تغيير الرمز |
| `family.notifications` | Notifications | الإشعارات |
| `family.notifications.detail` | Only "A request is waiting" is sent, never what was asked for. | لا يُرسل إلا «يوجد طلب بانتظارك»، ولا يُرسل أبدًا ما طُلب. |
| `family.child.limit` | Content limit | حدّ المحتوى |
| `family.child.unrated` | Unrated content | المحتوى غير المصنَّف |
| `family.child.unrated.hide` | Hide | إخفاء |
| `family.child.allowOnly` | Only allowed items | العناصر المسموح بها فقط |
| `family.child.allowed` | Allowed | مسموح |
| `family.child.blocked` | Blocked | محظور |
| `family.child.devices` | Devices | الأجهزة |
| `family.child.activity` | Activity | النشاط |
| `family.child.guardians` | Guardians | الأولياء |
| `family.child.lift` | Lift restriction | رفع التقييد |
| `family.child.lift.confirm` | %@ will see everything their libraries hold. | سيرى %@ كل ما تحويه مكتباته. |
| `family.approve.today` | Today | اليوم |
| `family.approve.week` | This week | هذا الأسبوع |
| `family.approve.always` | Always | دائمًا |
| `family.deny` | Deny | رفض |
| `family.device.pending` | Asks for a looser limit: %@ | يطلب حدًّا أوسع: %@ |
| `family.device.approve` | Approve | موافقة |
| `family.makeRestricted` | Make an account restricted | تقييد حساب |
| `family.rateLibraries` | Rate libraries | تصنيف المكتبات |
| `family.rateLibraries.unrated` | %d not rated | %d غير مصنَّف |

## Edge cases

- **A cloud sign-in that is play-scoped**: Family is read-only (writes answer 403 from the home);
  the controls show disabled with "Connect at home or ask an admin for manage access".
- **Two guardians edit at once**: the policy's `version` makes the second write 409; the screen
  reloads and shows the other guardian's change.

| Key | English | Arabic |
|---|---|---|
| `family.readOnlyCloud` | Connect at home, or ask an admin for manage access, to change this. | اتصل من المنزل، أو اطلب من المشرف صلاحية الإدارة، لتغيير هذا. |
| `family.conflict` | Someone else changed this. Showing the latest. | غيّر شخص آخر هذا. يُعرض أحدث إصدار. |

## Build plan

| File | Change |
|---|---|
| `apps/apple/Player/Family/` (new): `FamilyView.swift`, `ChildView.swift`, `RulesView.swift`, `DevicesView.swift`, `RequestsView.swift`, `PinView.swift`, `RateLibrariesView.swift`, `Family.xcstrings` | screens |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Parental.swift` (new) | API client and models |
| `apps/apple/Player/Profile/ProfilePage` wiring | the row |
| `apps/apple/UITests/MockHome.swift`, `Fixtures/` | guardian and admin fixtures |
