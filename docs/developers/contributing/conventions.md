# Conventions

These rules apply to every change, in every service and app. Reviewers check them, and
several are enforced by CI.

## English and Arabic ship together

Every user-facing string ships in English and Arabic in the same change, on every
surface: the web app, the Android apps and the Apple apps. A string in one language
only is an unfinished change.

| Surface | Where strings live |
|---|---|
| Web | `web/src/i18n/en.json` and `web/src/i18n/ar.json`, used through `t("key")` |
| Android | `res/values/strings.xml` and `res/values-ar/strings.xml` in each module |
| Apple | the `.xcstrings` catalogs (for example `apps/apple/Player/Localizable.xcstrings`), with both `en` and `ar` |
| User guide | the help pages in `web/src/help/`, in both languages |

RTL must work:

- Use logical layout (leading and trailing, `start` and `end`, CSS logical properties
  such as `margin-inline-start`) instead of left and right.
- Mirror directional icons, keep media controls and timelines in their natural
  direction, and test screens in Arabic.
- Text that mixes Arabic and Latin (titles, file names, numbers) must render with
  correct bidi; do not build sentences by concatenating fragments.

## Tests ship with the code

A change to app or service code updates its tests in the same change and the same push.
The affected-tests gate blocks a push whose changed sources have no changed tests, or
whose tests still look for strings or identifiers the code removed. A test that fails
after your change is stale, not flaky: fix it in the same change. See
[Running the tests](tests.md).

## Roles are enforced by the API

Every role rule (who may manage libraries, users, settings, devices, and so on) is
enforced server-side in the API, and each rule has a test that calls the endpoint as a
role that must be refused and checks the refusal. Hiding a button in the web or native
UI is not a rule; it is a convenience on top of one. A new endpoint without a role check
and a role test is incomplete.

## No secrets in code

- Never commit secrets, credentials, tokens, private keys or signing material, in code,
  tests, fixtures, docs or CI files.
- Configuration comes from the environment or from files outside the repository;
  `.env.example` shows names and safe defaults only.
- Tests generate their own throwaway keys and use obvious dummy values.
- Docs use generic examples: no personal home paths, private network addresses or
  machine names.

More in [Security rules for plugins and contributors](../security/conformance.md).

## Code style

- **Go:** `gofmt`, `go vet` and `golangci-lint`. One module per service (`api/`,
  `streaming/`, `cloud/`, each `cmd/` binary).
- **Python:** `ruff` and `ruff format`, `mypy --strict` (including tests).
- **Web:** eslint with zero warnings, `tsc --noEmit`, prettier.
- **Logs:** structured logging; never concatenate runtime data into a log message
  (`make lint-log` checks this).
- **Migrations:** append-only, idempotent, with Postgres and SQLite versions together
  (`make lint-migrations`).
- **Design tokens:** colours, spacing and type come from `web/design-system/tokens/`;
  generated `Tokens.swift` and `Tokens.kt` stay in sync (`make test-tokens`).

`make format` formats everything; `make lint` runs every linter as CI does.

## Commits

Commit subjects follow a conventional style: a type, an optional scope, and a short
summary in plain words.

```text
feat(apps): sign-in request banner and notification
fix(android): the waiting banner keeps the screens' state
test(tvos): the code screen names the approve step
docs(spec): Epic 50 home broadcasting
chore(devportal): format, and the changelog line
```

Common types: `feat`, `fix`, `test`, `docs`, `chore`, `refactor`, `perf`, `ci`. Common
scopes are the service or app (`api`, `streaming`, `pipeline`, `web`, `android`,
`apple`, `tvos`, `cloud`, `devportal`, `spec`).

- Keep each commit focused; explain why in the body when it is not obvious.
- When a change rightly needs no test change, add the trailer
  `No-Test-Change: <why>`.
- Reference the story (for example `Story 22.8`) or issue the change belongs to.

See [Pull requests](pull-requests.md) for the PR itself.
