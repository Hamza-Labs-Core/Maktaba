# Story 57.15 — Time limits on every client

> Epic 57 · Phase 3b · Depends on: 57.14 · Needed by: 57.13
> **Status:** built (checked against the code 2026-11-12) on iPhone, iPad, Apple TV, the Android Player (phone
> and tablet), Android TV, the web and the desktop Player.

## Why

The home enforces time (57.14), so no client can skip it. But a client that does not know about
it shows a broken player when the home says no. Every client has to show the child how much time
is left and say plainly when it is over, in English and Arabic. And a home with no iPhone needs
a place to set the rules.

## Description

### 1. What every client shows

For a restricted account, on the Apple Player (iPhone, iPad, Apple TV), Android (phone, tablet,
TV), the web and the desktop Player:

- **Time left**, from `GET /api/me/parental/time`, on Home and in the player controls once 15
  minutes or less remain;
- **a warning** 5 minutes and 1 minute before the end;
- **a time's-up screen** when the home answers `parental-time` on opening a session, opening it
  again, or a progress post: the reason, when watching opens again, and the saved position.
  Playback stops; the client does not retry by itself. A progress post that answers 404 means
  the stream has ended (the video was hidden): the client stops the same way;
- progress is posted while playing, at least every 60 seconds (the Apple Player posts every 10
  seconds, and at once on play, pause and leaving the front): it is the stream's heartbeat
  (57.14);
- the client never keeps its own count. It shows what the home says.

### 2. Asking for more

- Every client: the time's-up screen has **Ask for more time** (15, 30, 60 minutes) and, when
  someone has a PIN set, **Enter parent PIN**, through 57.06. A parent approves from their own
  iPhone or iPad Player, or on the web.

### 3. Setting the rules

- Apple Player: Family > the child > **Time** (with 57.07): rules by day, slot, daily limit and
  library; today's usage; a button to give more time.
- Web: `/family/{id}/time` (Family > the child > **Time**), for an admin or a guardian, with the
  same fields. Android and the desktop Player have no rules editor.

### 4. Strings

Every string ships in English and Arabic in each client's own catalog, and each screen is checked
mirrored.

| Key | English | Arabic |
|---|---|---|
| `parental.time.left` | %@ left today | بقي %@ اليوم |
| `parental.time.up.title` | Time's up for today | انتهى وقت اليوم |
| `parental.time.outside.title` | Not watching time yet | لم يحن وقت المشاهدة بعد |
| `parental.time.next` | You can watch again at %@ | يمكنك المشاهدة مجددًا عند %@ |
| `parental.time.askMore` | Ask for more time | اطلب وقتًا إضافيًا |
| `parental.time.askParent` | Ask a parent for more time | اطلب من أحد والديك وقتًا إضافيًا |
| `parental.time.rules` | Time | الوقت |
| `parental.time.daily` | Daily limit | الحد اليومي |
| `parental.time.slot` | Watching hours | ساعات المشاهدة |

## Acceptance criteria

- **Given** 5 minutes left, **then** each client shows the warning.
- **Given** the home answers `parental-time` mid-video, **then** each client stops, shows the
  time's-up screen with the next time, and does not retry.
- **Given** a grant arrives, **then** the Apple Player goes on without a restart; other clients
  go on at the next tap.
- **Given** Arabic, **then** every screen is mirrored and every string is Arabic.
- **Given** a guardian on the web, **then** they can set a rule; a viewer cannot see the page
  (and the API refuses, 57.14).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest | `MockHome` time states | model | left, warning, up |
| T02 | Player UI test, iPhone and iPad sims, en and ar | time up mid-play | — | screen shown; no retry |
| T03 | Android unit and Robolectric (phone and TV roots) | `parental-time` problem | play | screen shown, en and ar |
| T04 | web Vitest and Playwright | time up; rules editor as guardian and viewer | — | screen; editor; no page for a viewer |
| T05 | desktop Player test | `parental-time` | play | screen shown |
| T06 | string checks in each client | — | — | every key has Arabic |

## Edge cases

- **An old client** that does not know the problem type shows its generic playback error. The
  home still enforces.
- **Offline**: there are no downloads, so nothing plays without the home.

## Build plan

| File | Change |
|---|---|
| `apps/apple/MaktabaKit/…`, `apps/apple/Player/…` | time model, screens, Family > Time |
| `apps/android/…` (phone and TV) | time state and screen |
| `web/src/…` | time state, screen, Users > Time |
| the desktop Player | time state and screen |
| each client's string catalog | English and Arabic |
