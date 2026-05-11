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
make prereqs         # checks tools and prints install commands for anything missing
make dev             # bring up the live-reload stack (postgres, chroma, api, streaming, pipeline, web)
make test            # unit tier — no network, no sudo
make help            # list every target, grouped by section
```

That's the full day-1 loop. CI runs the **same** `make` targets you
run locally — see [`CONTRIBUTING.md`](CONTRIBUTING.md) for the canonical
dev workflow, troubleshooting, and pre-commit setup.
