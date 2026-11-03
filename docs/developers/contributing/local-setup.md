# Local setup

The canonical day-one setup is a Docker-based live-reload stack driven by `make`. CI
runs the same `make` targets, so what passes locally passes in CI.

## Prerequisites

Run `make prereqs` to check your machine. It lists each tool with the version found and
prints install commands for anything missing.

| Tool | Version | Used for |
|---|---|---|
| Docker | 24+ | the dev stack's containers |
| Docker Compose | v2.27+ | healthchecks and named profiles |
| Git | 2.40+ | working tree checks and the hooks |
| Go | 1.26+ | `api/`, `streaming/`, `cloud/`, `cmd/`, the tools |
| Python and uv | Python 3.12+, uv 0.4+ | `pipeline/` (uv manages the virtualenv) |
| Node and pnpm | Node 20+, pnpm 9+ | `web/` |
| FFmpeg | 6+ | streaming and the media tests |

Recommended: `golangci-lint`, `goose` (migrations), `pre-commit`, `jq`, `shellcheck`.

Postgres 16 runs in Docker through `make dev`. Do not install Postgres on the host: it
collides on port 5432 and skips the migrations the stack applies.

Both Apple Silicon and Intel work. The MLX transcription backend needs Apple Silicon;
elsewhere the pipeline falls back to another Whisper backend.

For the native apps you also need the platform toolchain: Xcode for `apps/apple/` and
`apps/tv/tvos/`, the Android SDK and JDK 17 for `apps/android/` and `apps/tv/`, and Rust
for the Tauri desktop app.

## First run

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba
cp .env.example .env     # change ports here if the defaults clash
make prereqs
make dev                 # builds the dev images and starts the stack
make test                # unit tier
```

A cold `make dev` pulls and builds images and takes a few minutes; later runs are
faster.

## The dev stack

`make dev` starts Postgres, Chroma, the API, streaming, the pipeline and the web app:

| Service | Address |
|---|---|
| API | http://localhost:8080 |
| Streaming | http://localhost:8081 |
| Web | http://localhost:5173 |
| Postgres | `postgres://maktaba:maktaba@localhost:5432/maktaba` (dev-only credentials) |
| Chroma | http://localhost:8000 |

Edits reload on their own: `air` rebuilds Go services, `watchmedo` restarts the
pipeline, and Vite hot-reloads the web app.

Useful targets (`make help` lists them all):

| Target | What it does |
|---|---|
| `make dev-logs` | Tail logs from every service |
| `make dev-ps` | Show the stack's status |
| `make dev-down` | Stop the stack, keep volumes |
| `make dev-clean` | Stop the stack and wipe volumes (resets the database) |
| `make dev-build` | Rebuild dev images after a `Dockerfile.dev` change |
| `make migrate` | Apply migrations against `$DATABASE_URL` |
| `make lint` | Every linter, as in CI |
| `make format` | Format every language in place |

To run the production compose stack without live reload, use `make compose-up`, or
`docker compose -f deploy/compose/docker-compose.yml up --build`.

## Running services on the host

You can also run the services as processes instead of containers, against the Postgres
from `make dev`:

- `make server-dev` builds `maktaba-server` without the embedded web UI, for fast
  compiles; `make server` builds it with the UI.
- `tools/dev/stack.sh` starts the API, streaming and pipeline workers on one machine.
  Read its header first: it takes the database URL, log folder and other settings from
  environment variables, and its defaults suit one developer setup, so set them for
  yours.
- In `web/`, `pnpm install` and then `pnpm dev` serves the web app with Vite.

## Hooks

Install these once per clone:

```sh
pre-commit install                  # fast lint on staged files
tools/affected-tests/install-hook.sh   # pre-push: changed code needs changed tests
```

See [Running the tests](tests.md) for what the pre-push hook does.

## Troubleshooting

- **Postgres state is wedged after a schema experiment:** `make dev-clean && make dev`.
- **A dev image will not pick up a Dockerfile change:** `make dev-build`.
- **Hot reload does not reach the browser:** set `VITE_HMR_HOST` in `.env` to the
  hostname your browser uses.
- **Behind a proxy:** the usual `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` variables are
  respected by Go, uv, pnpm and curl; `MAKTABA_REGISTRY_MIRROR` in `.env` points Docker
  at a mirror.
- **CI and local results differ:** that is a parity bug; open an issue with the diff.
