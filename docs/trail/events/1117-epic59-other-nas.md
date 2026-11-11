---
schema: 1
kind: pr
pr: 1117
title: "The plain compose as the NAS base, the Unraid template, maktaba.tv's install page (story 59.11)"
date: 2026-11-10
session: 6f796c2b-9b67-4e8c-b813-96dbbcad3b7b
agent: claude-code
---

## What happened
- #1117 (merged at 78c98f4e9) makes the published Docker compose (`cloud/installers/docker/docker-compose.yml`) the NAS base. It now has `container_name: maktaba`, `MAKTABA_PLATFORM: docker`, `TZ`, `user:` with how to find it (`id <user>`), and the media at its own host path (`/srv/media:/srv/media` instead of `${MAKTABA_MEDIA:-./media}:/media`; a comment tells earlier installs to keep their `/media` line). Its optional settings are commented blocks headed `# [name]`: `memory` (`mem_limit` 4g, 6g or 16g by tier), `bridge` (ports 8088, 8080 and 8081, never 54329, plus `MAKTABA_HOST_IP`; turning it on also drops `network_mode: host`), `dbus`, `dri` and `nvidia`. Turning a block on means removing the `# ` in front of its settings. Release's `sed` to the pinned tag is unchanged.
- `tests/nas/install_files.py` and `web/packages/install/src/compose.ts` implement that block rule twice: in Python for CI, in TypeScript for the page. The plan job (`--docker`) runs `docker compose config` on the base with no block, with each block alone and with all of them. It also checks the Unraid template's schema, renders it to `docker run` arguments, checks it for drift against the compose file (image, network, stop timeout, data path, render device, env), and checks the dev compose's images against release's build-images matrix with `docker manifest inspect` (the plan job signs in to ghcr with `packages: read`).
- `deploy/nas/unraid/maktaba.xml` is the CA template: host network, `--user 99:100 --stop-timeout 90`, data at `/mnt/cache/appdata/maktaba` (the README table's `/mnt/user/appdata` would be shfs, which 59.02 refuses), media at the same `/mnt/user` path, optional `/dev/dri`, `TZ` and `MAKTABA_PLATFORM=unraid`. `deploy/nas/unraid/README.md` holds the CA submission text; submitting it is an owner action.
- Cells: `uid-unraid` now starts the template's rendered `docker run` (`"template": "unraid"`). The new `compose-plain` fills in the published file and runs it with `docker compose run --rm -T maktaba setup` and `up -d --pull never`; both have a new `platform` assertion. `landed` += 59.11.
- maktaba.tv/install/docker is `web/packages/install` (en+ar, `vite.install.config.ts`, `pnpm -C web build:install`): a form that fills the file in, in the page. A test asserts no fetch. Hosting it on maktaba.tv is left to 59.14.
- `cloud/installers/nas/` was removed. `plan-25-31-nas-support.md` now says "Replaced by Epic 59" (Arabic in `docs/wiki/db/ar/epic-25.json`). The dev compose's images are `ghcr.io/hamza-labs-core/maktaba-{api,streaming,pipeline,web}`, and its header says it is not for a NAS.

## What we learned
- The compose check found an old bug: the NVIDIA block could never be turned on. Its nested keys (`#   resources:`) were not matched as settings, so `deploy:` stayed empty (`services.maktaba.deploy must be a mapping`). A commented block is only as good as a CI step that uncomments it.
- `compose-plain` failed on arm64: `mem_limit: 6g` is Light there (the tier rule: arm64 under 8 GiB is Light), not Standard. The cell runs on amd64 only now, and the compose comment and the install page note the ARM rule.
- CodeQL flagged `startswith("https://maktaba.tv")` (incomplete URL substring sanitization), even in a test-side checker. The fix compares `urlparse(...).hostname`.
- Vitest denies `?raw` imports from outside `web/`. `web/vitest.config.ts` now allows `../cloud/installers/docker` and `../tools/plugin-sdk` (the existing flowchart alias).
- Main moved twice while the PR was green. Both times `tools/dev/merge-main.sh` stopped on `tests/nas/cells.json`'s `landed` line (`plugin-cache-grant`, then `poster-queue`). Each was resolved by keeping every entry.

## Keep doing
- Generate a NAS file from the base with one rule, and run every variant through `docker compose config` in CI.
- Before choosing a cell's tier assertion, check the tier rule for each architecture the cell runs on.

## Open
- Owner actions: submit the Unraid template to Community Applications (a forum support thread, then CA registration; the text is in `deploy/nas/unraid/README.md`), and host the install pages on maktaba.tv.
- 59.08 (networking) may change the `bridge` and `dbus` blocks. 59.09 (Synology) and 59.10 (TrueNAS) derive from the base with the same rule.
- The device checklist for Unraid 7 and a UGREEN N100 box (T05) is not run yet. QNAP stays marked untested.
