---
schema: 1
kind: pr
pr: 1546
title: "The NAS guides caught up with ports (59.08), failed updates (#1538) and cluster roles (59.13)"
date: 2026-11-23
session: a1e17a40-6168-4a7f-90a0-877ab55b9b71
agent: claude-code
prs: [1546, 1224, 1538, 1140]
---

## What happened
- An audit of every NAS guide against the code: the maktaba.tv/install pages, `docs/nas`, the packaging READMEs, maktaba.dev and the download copy. It found these stale docs, each fixed in #1546 in English and Arabic:
  - **Troubleshooting's "A port is taken"** still said every taken port moves and "nothing to fix". After #1224, a port set by `MAKTABA_*_PORT` is never moved and stops the start, the database port (54329) is never moved, and on a bridge Docker refuses the host port. The TrueNAS and Unraid guides and `docs/nas/networking{,.ar}.md` never named the port variables. The networking guide also said *any* taken port moves.
  - **No guide said what a failed update looks like.** After #1538 the outcome is kept (`db_kept`), undone (`db_undone`) or restored (`db_restored`), and the sessions the migrations wait on are ended. Troubleshooting gained `#update-failed` and `#update-disk`, linked from After setup, TrueNAS, Unraid, Docker and Synology.
  - **The NAS page said "Strong above"** 16 GB, with no word on what each tier does. It also never said a NAS can be the home, a machine of a home elsewhere (a Light NAS is warned and needs `--light-nas`), or storage only. After setup never mentioned the share step or This NAS's shares from 59.13 (#1140).
  - **The Unraid checklist had no step** for the guide's two Unraid screenshots. QNAP and UGREEN have none either, with no device to run one on.
  - **maktaba.dev** covered 59.13 but had nothing on the port variables or the update rollback.
- The install pages' visual references were re-rendered in CI, because every page that changed shifts its full-page screenshot.

## What we learned
- A feature PR that changes what a household sees (a new env variable, a new halt code) updated the code's own docs (`docs/updates.md`, the compose comments, `questions.yaml`). It did not update the static guides that describe the same behaviour. #1224 updated the Docker and Synology builders but not the TrueNAS, Unraid or Troubleshooting pages beside them.
- `ReconcilePorts` moves only the web, API and live TV ports. Postgres was never in the fallback range, so "a taken port moves" was wrong for 54329 even before #1224.
- Running `prettier --write` over `docs/` reformats tables and code-block indents that nothing in CI formats. Format only `web/`.

## Keep doing
- Checking each guide claim against the code that makes it true (`ports.go`, `portenv.go`, `tier.go`, `setup_node_nas.go`, the TrueNAS template), not against the spec.

## Stop doing
- Shipping a behaviour change for NAS households without grepping `web/packages/install/site` for the old behaviour's words ("moves", "goes back by itself").

## Open
- Real-device screenshots, still placeholders:
  - TrueNAS (6): truenas.md step 13.
  - Unraid (2): unraid.md step 11.
  - QNAP `create-application` and UGREEN `project`: no checklist and no device yet.
  - Synology (6): synology.md steps 19–23.
- No guide walks a NAS container through joining a home elsewhere as a full node. `maktaba-pipeline join` then `setup --node` is not built for the image.
