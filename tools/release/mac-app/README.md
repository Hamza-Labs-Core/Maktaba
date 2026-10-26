# Maktaba.app server payload (Epic 36, story 36.7)

`Maktaba.app` carries the whole server in `Contents/Resources/server`, so one Mac install runs
everything. This directory builds it.

| File | What it does |
|---|---|
| `payload.sh` | builds `build/mac-app/server` (layout below), signs it when there is an identity, writes the tauri overlay `build/mac-app/tauri.mac-app.conf.json` |
| `check.sh` | smoke test of the payload and, with `--app`, of the built bundle |
| `build-app.sh` | payload → `tauri build` → check. `make mac-app` runs it |
| `tauri-build.sh` | `tauri build`, given the notarization settings only when the app can be notarized (a Developer ID and a complete set); `build-app.sh` and `desktop-release.yml`'s Mac legs build through it |
| `tauri-build-check.sh` | dry run of `tauri-build.sh`'s settings: the release without the Apple secrets, ad hoc, a Developer ID with and without a complete set |
| `pins.env` | the third-party downloads: version, URL, SHA-256 |
| `lib.sh` | the checked download (`fetch`) and the pins check, shared by `payload.sh` and `pins-check.sh` |
| `pins-check.sh` | dry run, offline: every pin is complete, and `fetch` keeps a matching download, re-fetches a bad cached one and stops on a checksum mismatch (`--online` also asks each URL) |
| `entitlements/python.plist` | the one binary that needs entitlements |

```bash
make mac-app            # Maktaba.app + .dmg, unsigned unless APPLE_SIGNING_IDENTITY is set
make mac-app-payload    # only the payload, then check.sh
```

Output: `apps/desktop/src-tauri/target/aarch64-apple-darwin/release/bundle/{macos/Maktaba.app,dmg/}`.
Needs macOS on Apple Silicon, Go, uv, pnpm (web deps installed), Rust.

## Layout

```
Contents/Resources/server/
  bin/       maktaba-server (web UI embedded) maktaba-api maktaba-streaming maktaba-launcher
  postgres/  bin/{postgres,initdb,pg_ctl,pg_dump,pg_dumpall,pg_restore,psql,pg_isready,
                  pg_upgrade,pg_controldata,pg_resetwal}  lib/  share/
  ffmpeg/    ffmpeg ffprobe
  ollama/    ollama llama-server          the language model server (arm64 only)
  python/    CPython 3.12 (bin/python3), stdlib precompiled
  pipeline/  maktaba_pipeline-<v>-py3-none-any.whl  constraints.txt
  licenses/  postgresql.txt postgresql-binaries.txt ffmpeg.txt python.txt ollama.txt ollama/
  payload.json   versions, sources, SHA-256s (the supervisor compares it across updates)
Contents/Library/LaunchAgents/com.hamzalabs.maktaba.server.plist   from the app shell (36.5)
```

Not in the app: the MLX / torch wheels (~1–2 GB) and the models. The first run builds the
venv from `pipeline/` with the bundled Python and downloads them (36.3); the language model
(`qwen2.5:7b`, ~4.7 GB) comes through the bundled Ollama (setup step `llm`, 36.11).

Size (2026-10-20, v0.2.0): payload 311 MB on disk — ffmpeg 125 MB, Go binaries 84 MB,
Python 73 MB, Postgres 27 MB, pipeline 1.5 MB. The `.dmg` (compressed) is what `check`
prints at the end of `make mac-app`.

## Where each piece comes from

| Piece | Source | Why | Licence |
|---|---|---|---|
| Go binaries | this repo, `make server build-go` (darwin/arm64, `-tags embed_web`, release ldflags) + the launcher | the same envelope as `release.yml` | this repo's |
| PostgreSQL 16.15 | [theseus-rs/postgresql-binaries](https://github.com/theseus-rs/postgresql-binaries) `aarch64-apple-darwin` tarball, SHA-256 from the release's `.sha256` (the release also has SLSA provenance) | relocatable (`@loader_path`, works from any path, spaces included), OpenSSL built in (pgcrypto), every contrib extension, pg_dump/psql. Major 16 = compose and CI. zonky's embedded build has no pg_dump/psql and 270 MB of ICU | PostgreSQL Licence (permissive); OpenSSL Apache-2.0 |
| pgvector 0.8.7 | [pgvector/pgvector](https://github.com/pgvector/pgvector) source tag `v0.8.7`, SHA-256 of the tag's archive; built by `payload.sh` against the PostgreSQL above (`pg_config`, pgxs, `PG_SYSROOT` = this Mac's SDK, `OPTFLAGS=""`: no `-march=native`) | semantic search's vectors in Postgres (`halfvec` + HNSW, Epic 43); `vector.dylib` links only libSystem and is signed with the rest | PostgreSQL Licence |
| ffmpeg / ffprobe 8.1.2 | [ffmpeg.martin-riedl.de](https://ffmpeg.martin-riedl.de) macOS arm64 release zips, SHA-256 from the site's `.sha256` | static (system frameworks only), 8.1 is what the home Mac runs | **GPLv3** (`--enable-gpl --enable-version3`, x264/x265) — see below |
| CPython 3.12.15 | [astral-sh/python-build-standalone](https://github.com/astral-sh/python-build-standalone) `install_only_stripped`, SHA-256 from the release's `SHA256SUMS` | relocatable, macOS 11+, what uv itself installs | PSF; bundled OpenSSL Apache-2.0, others in `python/lib/python3.12/LICENSE.txt` |
| Ollama 0.40.2 | [ollama/ollama](https://github.com/ollama/ollama) release `ollama-darwin.tgz`, SHA-256 from the release's `sha256sum.txt` | the language model server of the LLM stages (check-tags, check-clips, enrich); `ollama` and `llama-server` (llama.cpp with Metal) thinned to arm64, both linking only the system. The x86_64 CPU backends and the MLX engine (`mlx_metal_v*/`, ~400 MB, for MLX-format models only) are left out | MIT; llama.cpp and the rest it builds in: `licenses/ollama/` |
| pipeline wheel + constraints | `deploy/packaging/node/build-release.sh` (the node release's) | one build of the pipeline for every install | this repo's |

Bumping a piece: change `pins.env` (version, URL, SHA-256 from the publisher), `make mac-app`,
and the CI gate re-runs `check.sh`.

**GPL (ffmpeg).** ffmpeg runs as separate executables, so Maktaba's own code is not affected,
but distributing the binaries carries the GPL's source obligation: the release must offer the
corresponding source. `licenses/ffmpeg.txt` points at ffmpeg's source tarball and the build
scripts; mirroring `ffmpeg-8.1.2.tar.xz` as a release asset settles it for good (follow-up).

**Minimum macOS 15.** The Postgres build targets macOS 15 (`LC_BUILD_VERSION minos 15.0`), so
the arm64 app needs Sequoia or later (the overlay sets `LSMinimumSystemVersion` 15.0). Older
Apple Silicon Macs keep the Homebrew/node paths. Lowering it means building Postgres + OpenSSL
here with `MACOSX_DEPLOYMENT_TARGET=13.0`.

## Signing

`payload.sh` signs every Mach-O file one by one (no `--deep`): hardened runtime, secure
timestamp, the Developer ID identity in `APPLE_SIGNING_IDENTITY`. Only `python/bin/python3.12`
gets entitlements (`entitlements/python.plist`): pip installs MLX, torch and numpy after the
app is signed, so it loads code signed by other teams (library validation off) and maps JIT
memory. Postgres loads only its own extensions (same signature), the Go binaries and ffmpeg
need nothing. The stdlib is precompiled with unchecked hashes so Python never writes
`__pycache__` into the signed bundle (`check.sh` verifies nothing is written).

tauri then signs the app (seals `Resources/`), notarizes and staples it, and
`desktop-release.yml` notarizes and staples the `.dmg`. What the owner provides, as repository
secrets: `APPLE_CERTIFICATE` (base64 Developer ID Application .p12), `APPLE_CERTIFICATE_PASSWORD`,
`APPLE_ID`, `APPLE_PASSWORD` (app-specific), `APPLE_TEAM_ID`, optionally `APPLE_SIGNING_IDENTITY`;
and for the in-app updater `TAURI_SIGNING_PRIVATE_KEY(_PASSWORD)` with its public key in
`tauri.conf.json` (`plugins.updater.pubkey`). Without them the app is signed ad hoc and not
notarized, and the release still builds. tauri notarizes whenever the Apple ID or API key settings
are in its environment, even empty, which failed the macos-arm64 leg of v0.3.0-rc.7 and v0.3.1
("Team ID must be at least 3 characters"): `tauri-build.sh` hands them to tauri only for a
Developer ID app with a complete set, and CI's mac-app gate builds with them set but empty, as
the release does without the secrets.
