# Story 58.14 — Parental controls on the new TVs

> Epic 58 · Phase 5 · Depends on: Epic 57 stories 57.03, 57.05, 57.06, 57.10; 58.05, 58.06, 58.07, 58.08, 58.12 · Needed by: 58.17

## Why

Epic 57 put the parental filter in the home's API (story 57.03). Every client gets it, including
ones whose screens come later. A restricted child signed in on any TV of this epic therefore
already sees only allowed items, through the LAN, the relay or a cast, with nothing built on
the TV. This story covers what the TV must still do:
- show a blocked item as Epic 57 does;
- offer Ask a parent;
- make sure the new paths cannot route around the filter: the cast grant, the cloud code, deep
  links, and the Roku and Fire TV search integrations.

## Description

**What applies with no TV work:**
- Every content request from a TV goes through the home's `parental` filter (57.03).
- Blocked answers 404, the same as missing.
- Live channels and the guide are masked as 57.10 says.
- A restricted child's sign-in on a TV comes from a code approved by the child (their own
  account) or by a guardian with `as_user` (57.02). Either way it is restricted.

**What each TV adds** (the Apple TV's 57.11 pattern):

| Piece | Fire TV / Android TV | Samsung, LG (`/tv/`) | Roku | Cast receiver |
|---|---|---|---|---|
| Blocked page with **Ask a parent** (57.06's request API) | yes | yes (in `bigscreen`, so `/big` gets it too) | yes | no: the sender shows it (the receiver never opens what the sender could not) |
| Parent PIN on the TV | no (slow on a remote; a guardian answers from the Player) | no | no | no |
| "Managed by" line and the limit, in Account | yes | yes | yes | no |
| Device age signal | none: no TV platform here gives an app an age or rating limit (research.md §1.3) | none | none | none |

**New paths, closed:**
- **The cast grant** (58.06) is minted only for an item the sender's principal may see. It
  carries the principal's effective limit, and every session it opens is re-checked by
  `parental.Allowed`. A restricted child cannot use a grant to reach a blocked item, and a grant
  minted before a guardian tightens the limit stops opening new items within one policy refresh
  (57.03's `pg_notify`).
- **The cloud code** (58.03) signs the TV in as the approver. A restricted child approving gives
  a restricted TV. The approve screen never lets a child approve "as" someone else.
- **Deep links** (Roku 58.12, Fire TV's `SEARCH` intent): they open by ID through the same
  filtered routes, so a blocked ID gives the not-available line, never a hint that it exists.
- **Store search integrations** are not built (Alexa video skill, Roku's search feed). If they
  ever are, they must not publish a home's titles outside the home, so this epic leaves them out.

**The bypass rehearsal** of 57.13 (A1–A22) gains rows for this epic, run in 58.17:

| # | Attempt | What stops it |
|---|---|---|
| A23 | Use a cast grant for a blocked item | the grant names items; sessions re-checked |
| A24 | Mint a grant, then the guardian tightens; open the next queue item | re-checked per session against the live policy |
| A25 | A restricted child approves a cloud code "for" the parent's account | the TV is signed in as the approver only |
| A26 | Deep link to a blocked video ID on Roku or Fire TV | 404 as missing; not-available line |
| A27 | The TV UI at `/tv/` loaded on a phone browser to dodge a TV-only check | there is no TV-only check: the filter is on the home |

## Acceptance criteria

- **Given** a child's account at 9+ signed in on a Samsung TV, **then** no 13+ or unrated item
  appears anywhere in `/tv/`, and a 13+ item opened by a link shows the blocked page with Ask a
  parent.
- **Given** the child asks on Roku, **then** the guardian's Player shows the request, and on
  approval the item plays on the Roku without a restart.
- **Given** a guardian casting from their own phone, **then** the cast plays what the guardian may
  see. A child casting from their phone gets only allowed items.
- **Given** Arabic, **then** the blocked page and the managed line are mirrored on each TV.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go Postgres (`parental-bypass` target of 57.13) | A23–A27 | run directly and through the relay | each refused as listed |
| T02 | Robolectric (Fire TV / Android TV) | restricted fixture | open a blocked item | blocked page, Ask a parent focused; en and ar |
| T03 | Playwright `/tv/` | restricted fixture | same | same |
| T04 | brs-engine (Roku) | restricted fixture | deep link to a blocked ID; open blocked | not-available line; blocked page |
| T05 | Go Postgres | cast grant minted, then policy tightened | open next queue item | 404 |

## Strings

The TVs reuse Epic 57's `parental.blocked.*` keys in their own catalogs (Android TV and `/tv/` from
the shared catalogs; Roku through 58.15's build step), plus 57.11's
`tv.parental.askOnPhone`:

| Key | English | Arabic |
|---|---|---|
| `tv.parental.askOnPhone` | A parent can answer from Maktaba on their phone. | يمكن لأحد الوالدين الرد من Maktaba على هاتفه. |

(57.11's Apple TV wording names iPhone and iPad; these TVs serve Android and iPhone families
alike, so the line says "phone".)

## Edge cases

- **A TV signed in as a parent and used by a child**: the home cannot know who holds the remote,
  and no TV platform here tells the app. Help (Epic 57's 57.09 page) says to sign the TV in with
  the child's account, or to use the TV's own app lock.

## Build plan

| File | Change |
|---|---|
| `apps/tv/android/…/screens/Blocked*.kt` | blocked page, managed line |
| `web/src/bigscreen/screens/Blocked.tsx` | same, for `/tv/` and `/big` |
| `apps/tv/roku/components/Blocked/` | same |
| `api/internal/parental/` tests | A23–A27 in the bypass target |
