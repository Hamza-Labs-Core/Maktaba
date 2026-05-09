# Maktaba

[![CI](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Hamza-Labs-Core/Maktaba/actions/workflows/ci.yml)

Batch video transcription, subtitling, and intelligent search.

## Repository layout

| Path | What |
|---|---|
| `api/` | Go API server (Epic 7). |
| `streaming/` | Go streaming server (Epic 8). |
| `pipeline/` | Python transcription/indexing pipeline (Epics 1–6). |
| `web/` | Web frontend (Epic 11). |
| `shared/` | OpenAPI schema, DB migrations. |
| `specs/` | Epic and story specs (the design source of truth). |
| `docs/wiki/` | Generated wiki views over `specs/`. |
| `deploy/` | Compose stacks, Terraform for repo config. |

## Quickstart

```sh
make lint            # gofmt, golangci-lint, ruff, mypy, eslint, tsc, prettier
make test-unit       # unit tier across api, streaming, pipeline, web
make test-integration # needs DATABASE_URL + CHROMA_URL services up
make build           # cross-compile every artifact for the current host
```

CI runs the same `make` targets — see [`CONTRIBUTING.md`](CONTRIBUTING.md).
