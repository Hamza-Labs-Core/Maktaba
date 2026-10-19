# UI test cases

Test cases for every page in the web app (every route in `src/App.tsx`). These are cases,
not test code: each one says what to mock, what to do and what to expect, so it can be turned
into a vitest + Testing Library test (or a browser e2e test) later.

**47 pages, 1191 cases** (465 P1, 536 P2, 190 P3). Existing tests already cover 83 cases fully
and 164 partly; the other 944 have no test yet.

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
   link then works without a refresh. The four auth pages (`/login`, `/register`,
   `/forgot-password`, `/reset-password`) sit outside `AppShell`, so a boundary there does
   not cover them; their cases say so.

## Suspected bugs

While reading the code, the writers noted places that look broken (mostly unguarded
`null` lists). They are listed by page in [SUSPECTED-BUGS.md](SUSPECTED-BUGS.md). None of
them has been run or confirmed.

## Common test setup

- Signed out: `RequireAuth` (`src/lib/auth.tsx`) redirects to `/login` with `state.from`.
- Admin pages: `AdminGate` (`src/components/AdminGate.tsx`) shows the `common.forbidden.*`
  error state for non-admins. Some admin pages have no `AdminGate`; their Permissions cases
  say so.
- RTL: switch the language to Arabic (`src/lib/i18n`); `<html dir="rtl">`.
- Theme: `<html data-theme="light|dark">` (`src/lib/theme.ts`).
- Phone width: 375 px.

## Sign-in and app shell

| File                                     | Route                           | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------------------- | ------------------------------- | ------ | ----- | --- | ----------------------- |
| [login.md](login.md)                     | `/login`                        | LGN    | 27    | 9   | 2 / 5                   |
| [register.md](register.md)               | `/register`                     | REG    | 23    | 4   | 0 / 0                   |
| [forgot-password.md](forgot-password.md) | `/forgot-password`              | FPW    | 16    | 3   | 0 / 0                   |
| [reset-password.md](reset-password.md)   | `/reset-password?token=<token>` | RPW    | 20    | 5   | 0 / 0                   |
| [app-shell.md](app-shell.md)             | layout, `/`, `*`                | SHL    | 34    | 13  | 2 / 8                   |
| [more-menu.md](more-menu.md)             | `/more`                         | MOR    | 17    | 6   | 0 / 2                   |

## Library and playback

| File                                   | Route                                                 | Prefix | Cases | P1  | Covered (Yes / Partial) |
| -------------------------------------- | ----------------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [library.md](library.md)               | `/library`, `/library/:libraryId`                     | LIB    | 29    | 12  | 0 / 4                   |
| [series-browser.md](series-browser.md) | `/series`                                             | SER    | 20    | 6   | 1 / 2                   |
| [series-detail.md](series-detail.md)   | `/series/:id`                                         | SED    | 25    | 9   | 0 / 0                   |
| [video-detail.md](video-detail.md)     | `/videos/:videoId`, `/videos/:videoId?tab=enrichment` | VID    | 29    | 9   | 0 / 5                   |
| [video-player.md](video-player.md)     | `/videos/:videoId/watch`                              | VPL    | 29    | 15  | 4 / 4                   |
| [search.md](search.md)                 | `/search`, `/search?q=<encoded term>`                 | SRC    | 27    | 12  | 1 / 4                   |

## Live TV

| File                                                             | Route                        | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------------------------------------------- | ---------------------------- | ------ | ----- | --- | ----------------------- |
| [guide.md](guide.md)                                             | `/guide`                     | GDE    | 26    | 8   | 4 / 3                   |
| [live-player.md](live-player.md)                                 | `/live/:number`, `/live/1`   | LPL    | 31    | 11  | 8 / 11                  |
| [admin-live-tv.md](admin-live-tv.md)                             | `/admin/live-tv`             | LTV    | 36    | 13  | 7 / 10                  |
| [admin-live-tv-changes.md](admin-live-tv-changes.md)             | `/admin/live-tv/changes`     | LTC    | 23    | 7   | 5 / 7                   |
| [admin-live-tv-change-detail.md](admin-live-tv-change-detail.md) | `/admin/live-tv/changes/:id` | LCD    | 32    | 14  | 7 / 8                   |

## Processing queue

| File                                           | Route                                                  | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ---------------------------------------------- | ------------------------------------------------------ | ------ | ----- | --- | ----------------------- |
| [queue-overview.md](queue-overview.md)         | `/queue`                                               | QOV    | 30    | 15  | 3 / 10                  |
| [queue-detail.md](queue-detail.md)             | `/queue/:stage`, `/queue/fingerprint`                  | QDT    | 28    | 13  | 5 / 5                   |
| [queue-item-history.md](queue-item-history.md) | `/queue/items/:kind/:id`, `/queue/items/library/lib-1` | QIH    | 27    | 10  | 6 / 3                   |
| [queue-reports.md](queue-reports.md)           | `/queue/reports`                                       | QRP    | 23    | 10  | 8 / 4                   |
| [transcription.md](transcription.md)           | `/transcription`                                       | TRN    | 25    | 9   | 5 / 8                   |

## Settings

| File                                             | Route                                        | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ------------------------------------------------ | -------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [settings-account.md](settings-account.md)       | `/settings` (+ tab nav)                      | SAC    | 22    | 9   | 0 / 0                   |
| [settings-sessions.md](settings-sessions.md)     | `/settings/sessions`                         | SSE    | 9     | 2   | 0 / 0                   |
| [settings-appearance.md](settings-appearance.md) | `/settings/appearance`                       | SAP    | 20    | 8   | 0 / 8                   |
| [settings-privacy.md](settings-privacy.md)       | `/settings/privacy`                          | SPR    | 22    | 9   | 0 / 0                   |
| [settings-models.md](settings-models.md)         | `/settings/models`                           | SMD    | 24    | 10  | 0 / 0                   |
| [settings-rules.md](settings-rules.md)           | `/settings/rules`                            | SRL    | 32    | 15  | 8 / 7                   |
| [settings-about.md](settings-about.md)           | `/settings/about`, `/settings/about#updates` | SAB    | 26    | 12  | 1 / 3                   |

## Account and cloud

| File                                       | Route               | Prefix | Cases | P1  | Covered (Yes / Partial) |
| ------------------------------------------ | ------------------- | ------ | ----- | --- | ----------------------- |
| [account.md](account.md)                   | `/account`          | ACC    | 24    | 9   | 0 / 0                   |
| [account-devices.md](account-devices.md)   | `/account/devices`  | ADV    | 21    | 8   | 0 / 0                   |
| [account-sessions.md](account-sessions.md) | `/account/sessions` | ASE    | 21    | 10  | 0 / 0                   |
| [account-tokens.md](account-tokens.md)     | `/account/tokens`   | ATK    | 29    | 11  | 0 / 0                   |
| [billing.md](billing.md)                   | `/billing`          | BIL    | 24    | 9   | 0 / 0                   |
| [cloud-status.md](cloud-status.md)         | `/cloud/status`     | CST    | 23    | 11  | 0 / 0                   |

## Admin

| File                                         | Route                                             | Prefix | Cases | P1  | Covered (Yes / Partial) |
| -------------------------------------------- | ------------------------------------------------- | ------ | ----- | --- | ----------------------- |
| [admin-users.md](admin-users.md)             | `/admin/users`                                    | USR    | 27    | 13  | 0 / 0                   |
| [admin-library-acl.md](admin-library-acl.md) | `/admin/library-acl`                              | ACL    | 22    | 9   | 0 / 0                   |
| [admin-extensions.md](admin-extensions.md)   | `/admin/extensions`, `/admin/extensions#dizquetv` | EXT    | 23    | 10  | 3 / 5                   |
| [admin-enrichment.md](admin-enrichment.md)   | `/admin/enrichment`                               | ENR    | 21    | 9   | 0 / 1                   |
| [admin-channels.md](admin-channels.md)       | `/admin/channels`                                 | CHN    | 38    | 18  | 1 / 17                  |
| [admin-filler.md](admin-filler.md)           | `/admin/filler`                                   | FIL    | 24    | 10  | 0 / 3                   |
| [admin-analytics.md](admin-analytics.md)     | `/admin/analytics`                                | ANL    | 26    | 10  | 0 / 0                   |
| [admin-audit.md](admin-audit.md)             | `/admin/audit`                                    | AUD    | 28    | 9   | 0 / 0                   |
| [admin-health.md](admin-health.md)           | `/admin/health`                                   | HLT    | 25    | 10  | 0 / 1                   |
| [admin-machines.md](admin-machines.md)       | `/admin/machines`                                 | MCH    | 29    | 11  | 2 / 14                  |
| [admin-logs.md](admin-logs.md)               | `/admin/logs`                                     | LOG    | 27    | 9   | 0 / 2                   |
| [admin-devices.md](admin-devices.md)         | `/admin/devices`, `/account/devices`              | DEV    | 27    | 11  | 0 / 0                   |
