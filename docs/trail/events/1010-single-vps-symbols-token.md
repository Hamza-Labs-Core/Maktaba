---
schema: 1
kind: pr
pr: 1010
title: The single VPS's deploy writes the symbol uploaders' token
date: 2026-11-08
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [1007]
---

## What happened
- The `MAKTABA_SYMBOLS_TOKEN` repository secret (story 53.16) was added, but `relay.yml` passed it only to the Phase 1 pair's deploy step (`if: vars.CLOUD_HA == 'true'`). The single VPS's `deploy/cloud-relay/deploy.sh` never wrote the token file, so that api would have no `MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE`, and uploads would answer 503 `symbols_off`.
- The fix has three parts:
  - The single-VPS step now gets the secret.
  - `deploy.sh` writes it over SSH's stdin to `secrets/symbols.token`, before the rollout restarts the container. The file is 0600 and owned like `secrets/log.key`, it is replaced only when the token changed, and it is never echoed. `.env` names the file.
  - `docker-compose.yml` passes the setting to the container. An empty secret means no file, so uploads stay off.
- `tools/ci/deploy_secrets.py` fails when a token file the api reads, or a repository secret one deploy step gets, is missing from the other path. Each exemption has its reason. On main it named exactly this gap.
- The new test found a bug: `deploy.sh`'s EXIT trap (`[ -n "$KEY_FILE" ] && rm …`) made a run without `RELAY_SSH_KEY` (`make relay-deploy` from a laptop) exit 1 after a good deploy. Now an `if`.

## What we learned
- Nothing deploys to production yet. The `deploy to VPS` job is skipped on every run, tags included, because `RELAY_DEPLOY` is unset. The cloud's hostnames don't resolve. So no live endpoint could confirm the token. Merging deploys nothing: the deploy runs on a tag.
- The single-VPS stack runs `serve --role relay`, and `/v1/symbols` is served by `--role api`. The token there turns nothing on until that stack runs an api, or the pair goes live.
- An EXIT trap's last status becomes the script's exit status when the script ends without `exit`. `[ cond ] && cmd` in a trap fails a good run whenever `cond` is false.
- macOS's `chown` has no `--reference` and BSD `sed -i` needs a suffix. A deploy script tested on the Mac should use `ls -n`, `id` and rewrites that don't depend on GNU tools.

## Keep doing
- Check every secret the api reads against both deploy paths in CI (`deploy_secrets.py`) whenever a secret is added.
- Test deploy scripts against a fake `ssh` that runs the remote command locally.

## Open
- Decide which cloud path goes live first: `RELAY_DEPLOY` for the single VPS (which would also need an api service for uploads), or `CLOUD_HA` for the pair. Then confirm uploads from the deploy log or an authenticated probe.
