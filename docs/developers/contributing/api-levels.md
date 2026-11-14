# API levels: how a home and its apps stay compatible

A home updates itself, but each app updates on its store's clock: a Fire TV app when the
Amazon Appstore pushes it, a Roku channel when Roku does, a Samsung or LG app after the store
certifies it, which can take weeks. The API level is the contract that lets an app tell, before
it signs in, whether it and the home can work together, and say so plainly when they cannot.
The full rules are in
[`docs/api/levels.md`](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/api/levels.md).

## What a home publishes

`GET /api/discovery/home` needs no sign-in and carries:

```json
{
  "id": "…", "name": "Kitchen", "version": "0.3.141",
  "api_level": 1,
  "api_level_oldest": 1,
  "client_floor": {}
}
```

- **`api_level`:** this home's level. It rises by one when a response changes in a way an
  older app would get wrong (a field's meaning changes, a route goes, a stream format
  changes). Adding fields never raises it.
- **`api_level_oldest`:** the oldest level the home still answers correctly. A home keeps
  every level released in the last 12 months, so a TV app a year behind still works.
- **`client_floor`:** `{ "<app>/<platform>": "<version>" }`, the oldest build of each app the
  home serves, keyed as the `X-Maktaba-Client` header names them. It is empty unless a release
  must refuse an old build, for example after a security fix.

A home older than the contract sends none of these: read both levels as 0 and the floor as
empty.

## What an app does

An app ships with `min_api_level` (the oldest home it can use) and `built_for_api_level`, and
decides each time it reaches a home:

| Case | Page |
|---|---|
| `api_level` < `min_api_level` | **Update the home**, naming the home and its version: an admin is pointed to Admin → Updates, anyone else to the home's admin. No sign-in is attempted |
| `built_for_api_level` < `api_level_oldest`, or the app's version is below its `client_floor` entry | **Update this app**, in the platform's words |
| otherwise | carry on, and hide what `GET /api/capabilities` says is off |

[`shared/compat/vectors.json`](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/shared/compat/vectors.json)
holds the cases. Every app's copy of the decision runs them in its own tests and must reach the
same verdict. Versions compare by number (`1.10.0` is above `1.9.0`), a missing part is 0, and
a pre-release suffix ends the number.

A build below the floor that tries anyway gets `426` with `code: client_too_old` and the
`floor` at sign-in, refresh and the pairing token routes
([the problem type](https://maktaba.tv/problems/client-too-old/)). A request without the
`X-Maktaba-Client` header (the web) is never refused: the header is a label, never a right.
The home logs each refused app and platform once a day, so an admin sees in Logs which TVs need
an update.

## The platform names

`X-Maktaba-Client: <app>/<version> (<platform>; <device>)`. The smart TV platforms are
`tizen` (Samsung), `webos` (LG), `firetv`, `roku`, `vega` and `cast`, and the Samsung and LG
launcher is the app `tv-web`: `tv-web/1.4.0 (webos; tv)`.

## Changing a response

A Go test (`api/internal/compat/levelcheck`) diffs `shared/api/openapi.yaml` against the
current level's snapshot, `shared/api/levels/<n>.yaml`. It fails, naming each change, when a
route or success response is gone, a field is gone, a field's type changed, or a field that was
always sent may now be missing. To ship such a change:

1. raise `compat.Level` in `api/internal/compat/compat.go`;
2. add a `## Level <n>` entry to `docs/api/levels.md` saying what changed;
3. run `go run ./cmd/levelcheck -write` from `api/` to write the new snapshot.

After adding fields, the same command refreshes the current snapshot so the new fields are
protected too.
