# UI test cases

Test cases for every page in the web app (every route in `src/App.tsx`). These are cases,
not test code: each one says what to mock, what to do and what to expect, so it can be turned
into a vitest + Testing Library test (or a browser e2e test) later.

**48 pages, 1207 cases** (476 P1, 541 P2, 190 P3). 972 cases are covered by a test and 34
partly. Almost every P1 and P2 case has a test; most P3 cases (theme, minor layout) do not.
Cases a unit test can't check (true phone layout, real theme colours, real playback) say
`No (e2e-only)`. Cases that show a bug not fixed yet have an `it.fails` / `it.todo` test that
asserts the right behaviour, and their coverage says so.

## How each file is laid out

- Header: the route(s), the page component and its children, the API calls (method, path,
  client function), who can open it, and what the existing `*.test.tsx` files cover.
- A summary table: `ID | Title | Category | Priority | Covered by existing test?`.
- One section per case: priority, category, coverage, preconditions/data, steps, expected.

Case ids are `<PREFIX>-<NN>` (for example `LTV-12`). Prefixes are unique per file, so an id can
be quoted in a test name (`it("LTV-12: hosts null shows empty state", ...)`) and grepped.

**Categories:** Happy path, Empty state, Loading, Error, Permissions, Validation, Navigation,
Responsive, RTL, Theme, Accessibility.

**Priority:**

- P1: core flow, crash, data loss or security, including the two bug classes below and
  permission checks.
- P2: important states: errors, empty, validation, navigation.
- P3: polish: theme, minor responsive and minor accessibility.

**Covered by existing test?** `Yes (<file>)`, `Partial (<file>: what is missing)` or `No`.

## Two bug classes every page checks

1. **A list comes back `null`.** The admin Live TV page went blank because `/api/tv/cluster`
   returned `"hosts": null` instead of `[]`. Every page that fetches data has cases where
   each array field it reads is `null`, and where the key is missing. The expected result is
   an empty state, not a blank page.
2. **A page crash breaks Back.** After a page crashed, browser Back did not work until a
   refresh, because there was no error boundary. Every page has a case that makes the page
   throw while rendering. The expected result is a route error fallback, and Back or a nav
   link then works without a refresh. The auth pages (`/login`, `/register`) and the
   required password change after an admin reset sit outside `AppShell`, so a boundary there does
   not cover them; their cases say so.

## Suspected bugs

While reading the code, the writers noted places that look broken (mostly unguarded
`null` lists). They are listed by page in [SUSPECTED-BUGS.md](SUSPECTED-BUGS.md). The tests
written from these cases confirmed most of them; nearly all are now fixed, and the ones still
open are `it.fails` / `it.todo` tests.

## Common test setup

- Signed out: `RequireAuth` (`src/lib/auth.tsx`) redirects to `/login` with `state.from`.
- Admin pages: `AdminGate` (`src/components/AdminGate.tsx`) shows the `common.forbidden.*`
  error state for non-admins. Some admin pages have no `AdminGate`; their Permissions cases
  say so.
- RTL: switch the language to Arabic (`src/lib/i18n`); `<html dir="rtl">`.
- Theme: `<html data-theme="light|dark">` (`src/lib/theme.ts`).
- Phone width: 375 px.

## Sign-in and app shell

| File                         | Route            | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------- | ---------------- | ------ | ----- | --- | ----------------------- |
| [login.md](login.md)         | `/login`         | LGN    | 27    | 9   | 24 / 0                  |
| [register.md](register.md)   | `/register`      | REG    | 23    | 4   | 19 / 0                  |
| [app-shell.md](app-shell.md) | layout, `/`, `*` | SHL    | 34    | 13  | 26 / 2                  |
| [more-menu.md](more-menu.md) | `/more`          | MOR    | 17    | 6   | 12 / 0                  |

## Library and playback

| File                                   | Route                                                 | Prefix | Cases | P1  | Covered (Yes / Partial) |
| -------------------------------------- | ----------------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [library.md](library.md)               | `/library`, `/library/:libraryId`                     | LIB    | 29    | 12  | 25 / 0                  |
| [series-browser.md](series-browser.md) | `/series`                                             | SER    | 20    | 6   | 15 / 0                  |
| [series-detail.md](series-detail.md)   | `/series/:id`                                         | SED    | 25    | 9   | 21 / 0                  |
| [video-detail.md](video-detail.md)     | `/videos/:videoId`, `/videos/:videoId?tab=enrichment` | VID    | 29    | 9   | 25 / 0                  |
| [video-player.md](video-player.md)     | `/videos/:videoId/watch`                              | VPL    | 29    | 15  | 26 / 0                  |
| [search.md](search.md)                 | `/search`, `/search/video/:videoId`                   | SRC    | 43    | 23  | 38 / 1                  |

## Live TV

| File                                                             | Route                        | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------------------------------------------- | ---------------------------- | ------ | ----- | --- | ----------------------- |
| [guide.md](guide.md)                                             | `/guide`                     | GDE    | 26    | 8   | 22 / 0                  |
| [live-player.md](live-player.md)                                 | `/live/:number`, `/live/1`   | LPL    | 31    | 11  | 25 / 1                  |
| [admin-live-tv.md](admin-live-tv.md)                             | `/admin/live-tv`             | LTV    | 36    | 13  | 33 / 0                  |
| [admin-live-tv-changes.md](admin-live-tv-changes.md)             | `/admin/live-tv/changes`     | LTC    | 23    | 7   | 19 / 1                  |
| [admin-live-tv-change-detail.md](admin-live-tv-change-detail.md) | `/admin/live-tv/changes/:id` | LCD    | 32    | 14  | 29 / 1                  |

## Processing queue

| File                                           | Route                                                  | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------------------------- | ------------------------------------------------------ | ------ | ----- | --- | ----------------------- |
| [queue-overview.md](queue-overview.md)         | `/queue`                                               | QOV    | 30    | 15  | 26 / 3                  |
| [queue-detail.md](queue-detail.md)             | `/queue/:stage`, `/queue/fingerprint`                  | QDT    | 28    | 13  | 23 / 1                  |
| [queue-item-history.md](queue-item-history.md) | `/queue/items/:kind/:id`, `/queue/items/library/lib-1` | QIH    | 27    | 10  | 21 / 1                  |
| [queue-reports.md](queue-reports.md)           | `/queue/reports`                                       | QRP    | 23    | 10  | 18 / 2                  |
| [transcription.md](transcription.md)           | `/transcription`                                       | TRN    | 25    | 9   | 22 / 1                  |

## Settings

| File                                             | Route                                        | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ------------------------------------------------ | -------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [settings-account.md](settings-account.md)       | `/settings` (+ tab nav)                      | SAC    | 22    | 9   | 17 / 0                  |
| [settings-sessions.md](settings-sessions.md)     | `/settings/sessions`                         | SSE    | 9     | 2   | 4 / 0                   |
| [settings-appearance.md](settings-appearance.md) | `/settings/appearance`                       | SAP    | 20    | 8   | 15 / 0                  |
| [settings-privacy.md](settings-privacy.md)       | `/settings/privacy`                          | SPR    | 22    | 9   | 19 / 0                  |
| [settings-models.md](settings-models.md)         | `/settings/models`                           | SMD    | 24    | 10  | 21 / 0                  |
| [settings-rules.md](settings-rules.md)           | `/settings/rules`                            | SRL    | 32    | 15  | 28 / 1                  |
| [settings-about.md](settings-about.md)           | `/settings/about`, `/settings/about#updates` | SAB    | 26    | 12  | 23 / 0                  |

## Account and cloud

| File                                       | Route               | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ------------------------------------------ | ------------------- | ------ | ----- | --- | ----------------------- |
| [account.md](account.md)                   | `/account`          | ACC    | 24    | 9   | 18 / 2                  |
| [account-devices.md](account-devices.md)   | `/account/devices`  | ADV    | 21    | 8   | 15 / 3                  |
| [account-sessions.md](account-sessions.md) | `/account/sessions` | ASE    | 21    | 10  | 14 / 3                  |
| [account-tokens.md](account-tokens.md)     | `/account/tokens`   | ATK    | 29    | 11  | 23 / 2                  |
| [billing.md](billing.md)                   | `/billing`          | BIL    | 24    | 9   | 17 / 2                  |
| [cloud-status.md](cloud-status.md)         | `/cloud/status`     | CST    | 23    | 11  | 17 / 1                  |

## Admin

| File                                         | Route                                             | Prefix | Cases | P1  | Covered (Yes / Partial) |
| -------------------------------------------- | ------------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [admin-users.md](admin-users.md)             | `/admin/users`                                    | USR    | 27    | 13  | 20 / 0                  |
| [admin-library-acl.md](admin-library-acl.md) | `/admin/library-acl`                              | ACL    | 22    | 9   | 15 / 0                  |
| [admin-extensions.md](admin-extensions.md)   | `/admin/extensions`, `/admin/extensions#dizquetv` | EXT    | 23    | 10  | 19 / 0                  |
| [admin-enrichment.md](admin-enrichment.md)   | `/admin/enrichment`                               | ENR    | 21    | 9   | 13 / 0                  |
| [admin-channels.md](admin-channels.md)       | `/admin/channels`                                 | CHN    | 38    | 18  | 30 / 2                  |
| [admin-filler.md](admin-filler.md)           | `/admin/filler`                                   | FIL    | 24    | 10  | 19 / 0                  |
| [admin-analytics.md](admin-analytics.md)     | `/admin/analytics`                                | ANL    | 26    | 10  | 19 / 1                  |
| [admin-audit.md](admin-audit.md)             | `/admin/audit`                                    | AUD    | 28    | 9   | 22 / 0                  |
| [admin-health.md](admin-health.md)           | `/admin/health`                                   | HLT    | 25    | 10  | 20 / 0                  |
| [admin-machines.md](admin-machines.md)       | `/admin/machines`                                 | MCH    | 29    | 11  | 23 / 3                  |
| [admin-logs.md](admin-logs.md)               | `/admin/logs`                                     | LOG    | 27    | 9   | 19 / 0                  |
| [admin-devices.md](admin-devices.md)         | `/admin/devices`, `/account/devices`              | DEV    | 27    | 11  | 24 / 0                  |
