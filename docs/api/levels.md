# API levels

A home and the apps on a household's TVs and phones update on different clocks: the home
updates itself ([updates](../updates.md)), while a TV app updates when its store releases it,
which can take weeks. The API level is how the two tell whether they can work together
(Epic 58 story 58.01).

## The contract

`GET /api/discovery/home` (public, no sign-in) carries three fields:

| Field | Meaning |
|---|---|
| `api_level` | this home's level |
| `api_level_oldest` | the oldest level this home still answers correctly; an app built for a lower level must update |
| `client_floor` | `{ "<app>/<platform>": "<version>" }`, the oldest build of each app this home serves; empty by default |

A home older than the contract sends none of them. An app reads that as level 0, oldest 0,
and no floor; level 0 counts as compatible with every app built before the contract.

Each app ships with `min_api_level` (the oldest home level it can use) and
`built_for_api_level`. Each time it reaches a home, before it signs in:

| Case | What the app does |
|---|---|
| home `api_level` < the app's `min_api_level` | **Update the home**: a full-screen page naming the home and its version. An admin is told to update it in Admin → Updates, anyone else to ask the home's admin. No sign-in is attempted |
| the app's `built_for_api_level` < home `api_level_oldest`, or its version is below its `client_floor` entry | **Update this app**: a page saying how, in the platform's words |
| otherwise | carry on; features the home lacks are hidden through `GET /api/capabilities`, never shown and broken |

`shared/compat/vectors.json` holds the cases; every app's copy of the decision runs them and
must reach the same verdict (`api/internal/compat` is the Go one).

## When the level rises

The level rises by one each time a client-visible contract changes in a way an older app
would get wrong: a field's meaning changes, a route is removed, a stream format changes.
Adding routes, responses or fields does not raise it.

`shared/api/levels/<n>.yaml` is the response surface level `n` promised, generated from
`shared/api/openapi.yaml`. A Go test (`api/internal/compat/levelcheck`) diffs the document
against the current level's snapshot and fails, naming each change, when a route or a
success response is gone, a field is gone, a field's type changed, or a field that was always
sent may now be missing. To make such a change:

1. raise `compat.Level` in `api/internal/compat/compat.go` by one;
2. add a `## Level <n>` entry below saying what changed and what an app built for the level
   before gets wrong;
3. write the new snapshot: `go run ./cmd/levelcheck -write` from `api/`.

After a compatible change (new fields), the same command refreshes the current level's
snapshot, so the new fields are protected from then on. It refuses to overwrite a snapshot
the document is no longer compatible with.

## The window

A home keeps serving every API level released in the last 12 months: `compat.Oldest` never
rises faster than that, so a TV app a year behind still works. Raising it is a release note
line and an entry here naming the levels no longer served.

## The client floor

`compat.ReleaseFloor` is the floor a release ships, empty unless a home release must refuse
an old build, for example after a security fix. Its keys are the app and platform as the
`X-Maktaba-Client` header names them (`tv-web/webos`, `tv/roku`, `player/android`). A build
below it gets `426 client_too_old`, with the `floor`, at sign-in, refresh and the pairing
token routes; the app shows Update this app. A request without the header (the web) is never
refused: the header is a label, never a right. The home logs each refused app and platform
once a day, so an admin sees in Logs which TVs need an update.

## Levels

## Level 1

The first level (Maktaba 0.3, November 2026): the API as `shared/api/levels/1.yaml` records
it. Homes before it publish no level and read as level 0.
