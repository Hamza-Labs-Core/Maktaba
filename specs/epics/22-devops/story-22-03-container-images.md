# Story 22.3 — Container images and compose stack

The canonical self-host path is `docker compose up`. Compose must work
on Mac and Linux without local dependencies beyond Docker.

## Acceptance criteria

- AC1. Four images published per release: `maktaba/api`,
  `maktaba/streaming`, `maktaba/pipeline`, `maktaba/web` (built once,
  served by Caddy in prod).
- AC2. `deploy/compose/docker-compose.yml` boots the full stack
  (Postgres + Caddy + the four services) on a fresh host with one
  `docker compose up -d` command.
- AC3. `docker-compose.mac.yml` overlay bind-mounts host FFmpeg and
  exposes Apple Neural Engine to the Pipeline container. A "doctor"
  one-liner verifies the bind worked.
- AC4. Image sizes ≤ targets: api ≤ 60 MiB, streaming ≤ 80 MiB
  (FFmpeg static excluded), pipeline ≤ 1.2 GiB (Whisper + Chroma),
  web ≤ 30 MiB.

## Test cases

- TC1. Cold boot: `docker compose up -d` on a CI runner brings the
  stack to "all healthy" within ≤ 90 s.
- TC2. Mac overlay: on darwin/arm64, the compose-mac overlay produces
  a Pipeline container that successfully invokes MLX (verified by
  `maktaba-pipeline doctor`).
- TC3. Image size guard: a build that pushes any image past its size
  budget fails CI with the size delta.

## Edge cases

- EC1. Docker Desktop's file-system performance on Mac — bind mounts
  use `:cached` consistency for the media volume; documented.
- EC2. SELinux on Linux — bind mounts include `:Z` where required;
  documented and tested.
- EC3. Rootless Docker — compose works rootless; user-namespace
  remapping for the media volume is documented.
