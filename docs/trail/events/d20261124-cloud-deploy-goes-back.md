---
schema: 1
kind: decision
title: "The single cloud node's deploy can always go back, and its first rehearsal found two ways it could not"
date: 2026-11-24
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
prs: [1628]
---

## What happened
- The review of #1628 found that a failed deploy could not go back: `setup.sh` wrote the new tag into
  `.env` before `deploy.sh` read it as the previous one, so a rollback started the bad release
  again. Nothing saved the compose file, the Caddyfile, the web app or the migrations, and the
  tests ran after the only point that rolled back.
- `ops/rollout.sh` now saves the whole set first, pulls every image before any file changes, and
  restores on any failure, the tests' included. The migrations the deploy applied go back by their
  Down scripts (`maktaba-cloud migrate undo`), all checked before the first runs. A Down that would
  delete rows written since is not run and the schema is kept (a release's migrations only expand
  it); a contract migration or a failing Down holds the node.
- The new `single-node` job of the cloud rehearsal runs the real `setup.sh`, systemd unit and stack on
  the runner and makes each failure happen. Its first run found two bugs no reading had:
  - **`docker compose` lets the shell's environment win over `--env-file`.** `begin` runs with the new
    release's tag in its environment, so restoring a cut-off deploy ran the new release's image. The
    scripts now call compose with an empty environment.
  - **`cmp -s` compares sizes first, and a file in `/proc` has size 0.** The same boot looked like a
    reboot. Compare the text.
- **On QA**, a deploy made to fail its tests went back to exactly what QA served, and a full test with
  a stand-in home then passed and was committed (224 Mbit/s relayed at 44 % of one vCPU).
- A pass over the crash windows, before a second review, changed four more things: a restore reads
  the saved set alone (never the new, perhaps half-written one), settles the migrations once, drops
  the saved set in one rename, and undoes migrations a role applied by starting on its own.
- A changed `cloud.toml` or Caddyfile with the same image was never applied: compose does not look
  inside a bind mount. Each service's label now carries its file's hash.
- Production's gate let any branch run it, ignored the workflow's own file, and left Postgres and
  Redis on moving tags. It now needs main in both jobs (and the `prod` environment deploys from main
  only), and compares the digests of all five images and the hashes of the workflow, the guard and
  `deploy/cloud-relay`.

## What we learned
- A rollback that has never run is not one. The rehearsal found what the
  code's author and its reviewer both missed.
- Save what is there before the first script that writes, not before the first that deploys.

## Keep doing
- For each failure a deploy can have, assert that the node as a whole equals what it was (images, files,
  web page, migrations, health), not that a rollback command exited 0.

## Open
- Decisions `cloud-prod-secrets` (a key and a DNS token per environment) and
  `cloud-offnode-backups`. Issue #1665 (certificates and backups between deploys).
