# Repository layout

Maktaba is one repository holding every service, app, spec and tool. This page lists
the top-level folders. Browse them on
[GitHub](https://github.com/Hamza-Labs-Core/Maktaba).

## Services

| Folder | What lives there |
|---|---|
| `api/` | The Go API service: REST, GraphQL and WebSocket handlers (`api/internal/handlers/`), auth, roles, middleware, cluster and update logic. Its own `go.mod`. |
| `streaming/` | The Go streaming service: HLS/DASH origin, FFmpeg transcode and remux, live TV channels. Its own `go.mod`. |
| `pipeline/` | The Python pipeline (`maktaba_pipeline`): scanner, transcription, embeddings, indexer, job workers. Managed with `uv` (`pyproject.toml`, `uv.lock`); tests in `pipeline/tests/`. |
| `cloud/` | The optional cloud relay (Go): identity, server linking, the tunnel relay, push. Its own module, migrations and Dockerfile. |
| `cmd/` | Top-level binaries: `maktaba-server` (API, streaming and the web UI in one binary) and `maktaba-launcher` (starts and hands over the server, handles updates). |

## Clients

| Folder | What lives there |
|---|---|
| `web/` | The React web app (`web/src/`, with the user guide in `web/src/help/` and strings in `web/src/i18n/`), the design system and tokens (`web/design-system/`), the cloud web app, the wiki app, and packages under `web/packages/` (including `devportal`, which builds maktaba.dev). Managed with `pnpm`. |
| `apps/apple/` | SwiftUI apps: Maktaba Player for iPhone, iPad and Mac, Maktaba Remote for iPhone and iPad, and the shared `MaktabaKit` Swift package. |
| `apps/android/` | Jetpack Compose apps: Player and Remote, on a portable `client` module and a shared `core`. |
| `apps/tv/` | Maktaba TV for tvOS (Swift) and Android TV (Kotlin). |
| `apps/desktop-player/` | Compose for Desktop Player for Windows and Linux. |
| `apps/desktop/` | The Tauri desktop app for macOS, Windows and Linux that carries the server. |
| `apps/mobile/` | Older Capacitor wrappers for iOS and Android. |

## Shared code and contracts

`shared/` holds what more than one service uses:

- `shared/api/`: `openapi.yaml` and the GraphQL schema, the single source of truth for
  the API contract.
- `shared/db/migrations/`: numbered SQL migrations, Postgres and SQLite versions side by
  side.
- `shared/go/`, `shared/log/`, `shared/metrics/`, `shared/tracing/`, `shared/health/`:
  common libraries for logging, metrics, tracing and health.
- `shared/testtier/`: the cross-language test tiers, budgets and I/O guards.
- `shared/fixtures/`, `shared/testdata/`: fixtures used by tests across services.

## Specs, docs and tests

| Folder | What lives there |
|---|---|
| `specs/` | The design source of truth: `architecture.md` and one folder per epic under `specs/epics/` (a README, stories and plans). |
| `docs/` | Operator and developer docs: testing, updates, cluster, hardware, the generated wiki, and these developer guides under `docs/developers/`. |
| `tests/` | Cross-service suites: the end-to-end tier (`tests/e2e/`) and the perf suite (`tests/perf/`) that run against the compose stack. |

## Build, deploy and tooling

| Folder or file | What lives there |
|---|---|
| `Makefile` | The canonical entry points. CI calls the same targets. Run `make help` for the list. |
| `deploy/` | Docker Compose stacks (`deploy/compose/`), packaging, observability, cloud relay deploy and repository configuration. |
| `tools/` | Developer tooling: `affected-tests` (the tests-ship-with-code gate), `ci` (path-filter checks), `test-budget`, `migration-lint`, `log-lint`, `coverage-floor`, `release`, `dev` (local stack scripts), `rehearsal` and more. |
| `.github/` | CI workflows (`workflows/ci.yml` and its called workflows), path filters (`ci-paths.yml`) and the pull request template. |
| `.githooks/` | The `pre-push` hook that runs the affected-tests gate. |
| `pyproject.toml` | Root pytest config for the suites in `tests/`. The pipeline's own package config is `pipeline/pyproject.toml`. |

## Finding the code for a feature

1. Find the epic in `specs/epics/` and read its README.
2. The epic names the service that owns the state; look in that service's folder.
3. For API endpoints, start from `shared/api/openapi.yaml`, then the matching package in
   `api/internal/handlers/`.

See the [architecture overview](architecture.md) for how the pieces fit together.
