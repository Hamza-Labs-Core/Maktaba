# Story 58.01 — The compatibility contract: API levels, oldest supported apps and the update prompt

> Epic 58 · Phase 1 · Depends on: — · Needed by: 58.05, 58.07, 58.08, 58.09, 58.12, 58.17, 58.18

## Why

A home and the apps on a family's TVs update on different clocks. The home updates itself
(Epic 28, `docs/updates.md`). A Fire TV app updates when the Amazon Appstore pushes it, a Roku
channel when Roku does, and a Samsung or LG app only after the store certifies it, which can take
weeks. Today an app learns only the home's `version` string (`GET /api/discovery/home`, read by
`HomeProbe` in MaktabaKit and by the Android core). No rule says which app builds a home still
serves, and no app can tell a person that the home or the app is too old. With five more TV
platforms, each on its own store's release clock, "too old" will happen every week.

## Description

**One integer, the API level.** The home's API gets an `api_level`, starting at 1 with this story.
It goes up by one each time a client-visible contract changes in a way an older app would get
wrong: a field's meaning changes, a route is removed, or a stream format changes. Adding fields
does not raise it. The list of levels and what each changed is kept in `docs/api/levels.md`. A test
fails when the OpenAPI document changes a response shape incompatibly without raising the level
(it diffs `shared/api/openapi.yaml` against the previous level's snapshot,
`shared/api/levels/<n>.yaml`).

**`GET /api/discovery/home` gains** (no sign-in, as today):

| Field | Meaning |
|---|---|
| `api_level` | this home's level |
| `api_level_oldest` | the oldest level this home still answers correctly; an app built for a lower level must update |
| `client_floor` | `{ "<app>/<platform>": "<version>" }`, the oldest build of each app this home serves (empty by default; raised only by a home release that must refuse an old build, for example after a security fix) |

**The rule for the home:** a home keeps serving every API level released in the last 12 months
(`api_level_oldest` never rises faster than that), so a TV app that is a year behind still works.
Removing an old level is a release note line and a `docs/api/levels.md` entry.

**The rule for each app:** an app ships with `min_api_level` (the oldest home it can use) and
`built_for_api_level`. At start, and whenever it reaches a home:

| Case | What the app does |
|---|---|
| home `api_level` < app's `min_api_level` | **Update the home**: a full-screen page naming the home and its version; an admin sees "Update it in Admin → Updates", a viewer "Ask the home's admin to update Maktaba". No sign-in is attempted |
| app's `built_for_api_level` < home's `api_level_oldest`, or the app's version is below `client_floor` | **Update this app**: a page saying how, by platform ("Open the Amazon Appstore …", "Roku updates channels by itself; restart the Roku to fetch it now", and so on) |
| otherwise | carry on; features the home lacks are hidden through `GET /api/capabilities` (story 51.09), never shown and broken (no placeholder features) |

**The X-Maktaba-Client header** (story 44.2) gains the new platforms, `tizen`, `webos`, `firetv`,
`roku`, `vega` and `cast`, and the app `tv-web` (the Samsung and LG launcher, 58.09). The header
stays a label and never a right. The home logs a refused build once a day per app/platform
(`client_floor`), so an admin sees in Logs which TVs need an update.

**Apps outside this epic adopt it too.** The Android TV, Apple TV and phone apps read the same
fields. An older home without them answers with no `api_level`, which the apps read as level 0,
and level 0 counts as compatible with every app built before this story.

## Acceptance criteria

- **Given** a home at level 3 with `api_level_oldest` 2, **when** a Roku build for level 1
  connects, **then** it shows Update this app, with Roku's text, and makes no other request.
- **Given** a home without `api_level` (an older release), **when** any app connects, **then** it
  treats the home as level 0 and either carries on (min level 0) or shows Update the home.
- **Given** a change to `shared/api/openapi.yaml` that removes a response field, **when** CI runs, **then**
  the level check fails until `api_level` is raised and `docs/api/levels.md` says why.
- **Given** `client_floor` names `tv-web/webos` 1.4.0, **when** a 1.3.9 launcher signs in, **then**
  sign-in answers 426 `client_too_old` with the floor, and the launcher shows Update this app.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | discovery handler | GET | `api_level`, `api_level_oldest`, `client_floor` present; no sign-in needed |
| T02 | Go unit | OpenAPI diff tool with a removed field | run | fails naming the field; passes after the level is raised |
| T03 | Go Postgres | `client_floor` set | login, refresh, pairing token with an older `X-Maktaba-Client` | 426 `client_too_old`; a newer build passes; a missing header passes (the web) |
| T04 | Shared vectors `shared/compat/vectors.json`, run by Go, Kotlin, Swift, TypeScript and BrightScript (brs-engine) | the table above | decide | the same verdict in every language |
| T05 | Each TV app's UI test (Robolectric, Playwright, brs-engine) | mock home at level 99 / level 0 | start | Update the home / Update this app pages, English and Arabic, dark |

## Strings

Every app carries these keys in its own string format (Android `strings.xml`, the web-TV JSON
catalogs, Roku's translation files, the receiver's JSON); the English and Arabic are the same
everywhere.

| Key | English | Arabic |
|---|---|---|
| `compat.updateHome.title` | This home needs an update | يحتاج هذا المنزل إلى تحديث |
| `compat.updateHome.admin` | Update Maktaba on %1$s in Admin → Updates, then come back. | حدِّث Maktaba على %1$s من الإدارة ← التحديثات، ثم عُد. |
| `compat.updateHome.viewer` | Ask the person who runs %1$s to update Maktaba. | اطلب من مسؤول %1$s تحديث Maktaba. |
| `compat.updateApp.title` | Update this app | حدِّث هذا التطبيق |
| `compat.updateApp.firetv` | Open the Amazon Appstore, find Maktaba and choose Update. | افتح متجر Amazon Appstore، وابحث عن Maktaba واختر «تحديث». |
| `compat.updateApp.roku` | Roku updates channels by itself. Restart the Roku to get the update now. | يحدّث Roku القنوات تلقائيًا. أعد تشغيل جهاز Roku لتحصل على التحديث الآن. |
| `compat.updateApp.tizen` | Open Apps on the Samsung TV, find Maktaba and update it. | افتح «التطبيقات» على تلفاز Samsung، وابحث عن Maktaba وحدِّثه. |
| `compat.updateApp.webos` | Open the LG Content Store, find Maktaba and update it. | افتح متجر LG Content Store، وابحث عن Maktaba وحدِّثه. |

## Edge cases

- **A home rolled back** (Epic 28's rollback) to a lower level: the app reads the level each time
  it reaches the home, so it shows Update the home until the home is forward again. Tokens are
  kept.
- **The relay** answers `/api/discovery/home` from the home itself (it forwards to the loopback
  API), so the level is the home's, never the cloud's.
- **A cluster** (Epic 35) whose nodes are mid-update: the front node answers; nodes behind it are
  kept within one level by Epic 35's update order.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/discovery/home.go` | the three fields |
| `api/internal/compat/` (new) | the level constant, `client_floor` check middleware on sign-in routes |
| `tools/api/levelcheck/` (new) | the OpenAPI diff against `shared/api/levels/<n>.yaml` |
| `docs/api/levels.md`, `shared/api/levels/1.yaml` | the first level |
| `shared/compat/vectors.json` | the shared decision vectors |
| `api/internal/client/client.go` | the new platform and app names in its doc comment and tests |
