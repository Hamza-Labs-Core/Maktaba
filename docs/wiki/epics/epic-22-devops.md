# Epic 22 — DevOps and Delivery

> **Status:** spec + plans complete. **Source:** `specs/epics/22-devops/`.
> **Anchors:** [`architecture.md`](../../../specs/architecture.md) §12.4 (compose layout), §12.3 (doctor pattern), §8 (migrations), §11.5 (secrets distribution).

## Goal

A self-hoster gets running in one command and stays running across upgrades. Maintainers ship releases predictably without manual steps. CI proves every change before it lands; CD makes shipping an already-validated build a one-liner. This epic covers CI, build, packaging, release, install, upgrade, and rollback. It does **not** cover security hardening ([Epic 23](epic-23-security.md)) or operational observability ([Epic 21](epic-21-observability.md)).

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 22.1 | [CI pipeline](../../../specs/epics/22-devops/story-22-01-ci-pipeline.md) | [plan-22-01](../../../specs/epics/22-devops/plan-22-01-ci-pipeline.md) | Six parallel gates (lint / unit / integration / e2e / perf-ci / build-artifacts) → single `ci-success` rollup; force-merge override. |
| 22.2 | [Reproducible builds](../../../specs/epics/22-devops/story-22-02-reproducible-builds.md) | [plan-22-02](../../../specs/epics/22-devops/plan-22-02-reproducible-builds.md) | Byte-stable artifacts via `-trimpath`, uv lockfiles, pnpm lockfiles, ko/buildx, cosign signing, minisign verification. |
| 22.3 | [Container images & compose](../../../specs/epics/22-devops/story-22-03-container-images.md) | [plan-22-03](../../../specs/epics/22-devops/plan-22-03-container-images.md) | Four multi-arch images + compose stack with Caddy TLS, healthchecks, volumes, Mac/Linux overlays. |
| 22.4 | [Database migrations](../../../specs/epics/22-devops/story-22-04-database-migrations.md) | [plan-22-04](../../../specs/epics/22-devops/plan-22-04-database-migrations.md) | Append-only, idempotent migrations; CONCURRENTLY enforced on indexes; SQLite parity. |
| 22.5 | [Release management](../../../specs/epics/22-devops/story-22-05-release-management.md) | [plan-22-05](../../../specs/epics/22-devops/plan-22-05-release-management.md) | SemVer tagging; CHANGELOG gating; tag-driven release workflow; Homebrew formula rendering; version consistency across binaries. |
| 22.6 | [Upgrade & rollback](../../../specs/epics/22-devops/story-22-06-upgrade-rollback.md) | [plan-22-06](../../../specs/epics/22-devops/plan-22-06-upgrade-rollback.md) | Doctor pre-flight; rolling restart with graceful drain; version-jump guard; forward-compat invariant enforcement. |
| 22.7 | [Multi-platform packaging](../../../specs/epics/22-devops/story-22-07-multi-platform-packaging.md) | [plan-22-07](../../../specs/epics/22-devops/plan-22-07-multi-platform-packaging.md) | Homebrew (launchd), Debian/RPM (systemd), iOS/Android (Capacitor), macOS/Win/Linux desktop (Tauri), tvOS/Android TV (native). |
| 22.8 | [Local developer workflow](../../../specs/epics/22-devops/story-22-08-local-developer-workflow.md) | [plan-22-08](../../../specs/epics/22-devops/plan-22-08-local-developer-workflow.md) | `make dev` + live-reload (air, watchmedo, Vite HMR); CI runs same `make` targets as developers. |

## Cross-cutting decisions

### CI/CD

- **Six gates in parallel:** lint, unit, integration, e2e, perf-ci, build-artifacts → all feed `ci-success` rollup.
- **Wall-clock budget:** green PR ≤20 min (AC4 Story 22.1).
- **Docs-only PRs** skip heavy gates via `dorny/paths-filter`. **Fork PRs** skip e2e/perf-ci with explicit "needs maintainer rerun" comment.
- **Force-merge override:** `force-merge: <reason>` line in PR body, validated by CI, stored as audit trail.

### Build reproducibility

- **Go:** `-trimpath -ldflags='-buildid='` + vendored deps under `api/vendor/` and `streaming/vendor/`.
- **Python:** `uv lock` (frozen); `cibuildwheel` for per-platform reproducibility.
- **Web:** `pnpm` lockfile; Vite output deterministic.
- **Containers:** `ko` for Go images (deterministic by construction); `docker buildx --provenance=true --sbom=true` for Python image with `SOURCE_DATE_EPOCH`.
- **Signing:** `cosign sign` (keyless OIDC) for images; `minisign` for binaries; signed checksums in release.

### Release versioning

- Single `VERSION` file is source of truth; embedded into binaries via `-ldflags -X`.
- SemVer `MAJOR.MINOR.PATCH`; pre-release `v1.2.0-rc.N` supported.
- CHANGELOG.md (Keep-a-Changelog format); PR gate requires entry under `[Unreleased]` (exemption: `docs-only` label).
- Mobile/desktop/TV versioned separately (`mobile-vN.M.P`, etc.) but pinned to platform via `compatibleApiVersion`.

### Container images

- Four images per release: `ghcr.io/maktaba/{api, streaming, pipeline, web}` (multi-arch linux/amd64 + linux/arm64).
- Caddy reverse proxy at edge; routes `/api`, `/graphql`, `/ws` → api; `/stream` → streaming; `/` → web.
- **Image-size guards:** api ≤60 MiB, streaming ≤80 MiB, pipeline ≤1.2 GiB, web ≤30 MiB.
- Mac overlay binds FFmpeg + exposes Apple Neural Engine; `maktaba-pipeline doctor mac_mlx` verifies MLX + ffmpeg bind.
- Healthchecks: api/streaming 10 s, pipeline 30 s (doctor), postgres 5 s.

### Database migrations

- **Goose** runner; files in `shared/db/migrations/NNNN_<topic>.{sql,sqlite.sql}`.
- **Linter** enforces: append-only, idempotency guards (`IF NOT EXISTS`/`IF EXISTS`), `CREATE INDEX CONCURRENTLY`, SQLite parity.
- Bootstrap: `maktaba-api migrate up` (auto-run if `MAKTABA_AUTO_MIGRATE=true`); `--migrate-only` exits after migrations.
- **Doctor subcommand**: pre-flight estimate + per-table row-count check for accidental data loss.

### Upgrade path

- Canonical: `git pull && docker compose pull && docker compose up -d`. Forward-compat invariant ([Story 24.9](epic-24-data-integrity.md)) permits old binary to read new schema.
- Long-running migrations (>30 s) require `--accept-long-migration`.
- Rolling restart: `/admin/drain` flips readiness; in-flight requests complete; streaming holds segments open; pipeline finishes claimed jobs.
- **Rollback:** checkout tag + `docker compose up -d`; **never** runs down migrations (forward-compat makes them unnecessary).
- **Version-jump guard:** one-minor jumps only; `v1.0 → v1.2` requires intermediate `v1.0 → v1.1 → v1.2`.

### Packaging & install

- **macOS Homebrew:** `brew install maktaba/tap/maktaba` → three binaries + uv-managed pipeline venv + three launchd plists.
- **Linux (Debian/RPM):** `nfpm`; creates `maktaba` system user; `/lib/systemd/system/maktaba-{api,streaming,pipeline}.service`.
- **Mobile (iOS/Android):** Capacitor wrapping web bundle; `compatibleApiVersion` range check.
- **Desktop (macOS/Win/Linux):** Tauri bundles (`.dmg` notarized, `.msi`, `.AppImage`); auto-update opt-in via Tauri updater.
- **TV (tvOS/Android TV):** native apps (not Capacitor/Tauri); Xcode + Gradle builds; manual store upload in v1.

### Developer workflow

- `make dev` → live-reload (air for Go, watchmedo for Python, Vite HMR for web). Cold start ~5 min; warm ~90 s.
- File save → live in ≤5 s (Go), ≤5 s (Python), ≤1 s (web HMR).
- Canonical targets: `make lint`, `make test`, `make build`. CI runs the same — no CI-only scripts.
- Pre-commit hooks: gofmt, ruff, prettier, trailing whitespace, migration-lint.

## Files & code paths

- `.github/workflows/ci.yml`, `.github/workflows/_*.yml`, `.github/labeler.yml`, `deploy/github/branch-protection.tf`
- `tools/{build,sign,verify-reproducibility,upgrade,rollback,version-jump-guard,bump-version,render-formula}.sh`
- `tools/image-size-guard.sh`, `pipeline/.dockerignore`, `.ko.yaml`
- `deploy/compose/{docker-compose.yml, docker-compose.mac.yml, docker-compose.dev.yml, test.yml}`
- `deploy/docker/caddy/Caddyfile`
- `deploy/launchd/`, `deploy/packaging/{nfpm.yaml,systemd/}`
- `apps/mobile/build-mobile.sh`, `apps/desktop/src-tauri/tauri.conf.json`
- `shared/db/migrations/`, `tools/migration-lint.go`, `api/cmd/api/migrate.go`
- `Makefile`, `.pre-commit-config.yaml`, `CONTRIBUTING.md`, `.editorconfig`
- `VERSION`, `CHANGELOG.md`, `deploy/homebrew/Formulafile.tpl`

## API endpoints

- `GET /api/system/version` (story 22.5; also surfaced as `maktaba-api --version`).
- `GET /admin/drain` (story 22.6; readiness toggle for graceful shutdown).

## Dependencies

- **GitHub Actions:** CI runner; all third-party Actions pinned by SHA.
- **Goose v3** (migrations), **ko** (Go OCI), **cosign** + **minisign** (signing), **uv** (Python lockfiles), **pnpm** (web lockfiles), **cibuildwheel** (Python wheels), **nfpm** (deb/rpm), **Homebrew**, **Capacitor 6.x**, **Tauri 2.x**, **Caddy 2.x**, **Docker Compose v2.27+**, **Terraform** (branch-protection IaC).

## Out of scope

- Security hardening — [Epic 23](epic-23-security.md).
- Observability & audit log — [Epic 21](epic-21-observability.md).
- Data integrity & forward-back-compat semantics — [Epic 24](epic-24-data-integrity.md).
- Test tier definitions — [Epic 20](epic-20-testing.md) owns unit/integration/e2e structure.

## See also

- [Epic 23 — Security](epic-23-security.md) (signed artifacts, supply chain).
- [Epic 24 — Data Integrity](epic-24-data-integrity.md) (forward-back-compat invariants).
- [Migrations catalog](../migrations.md).
- [Glossary](../glossary.md) — CI gate, branch protection, force-merge, reproducible build, SBOM, signing, SemVer, release tag, multi-arch image, healthcheck, drain, rolling restart, version-jump guard, doctor.
