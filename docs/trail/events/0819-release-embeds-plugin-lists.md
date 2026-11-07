---
schema: 1
kind: pr
pr: 819
title: Each release embeds the plugin store's current lists, verified (Epic 54, #755)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [733, 778]
---

## What happened
- `release.yml` gains a `plugin-lists` job that runs `shared/plugins/cmd/plugin-lists` once per release: once the committed plugin root lists keys, it fetches `store-keys.json` and `revoked.json` from the store's CDN, verifies them by the chain of trust and hands them to every Go build of the release (one step after each "Embed the license-server key", four `needs` lists). Fails closed; while the root lists no keys it embeds nothing.
- The trigger (a tag push), the permissions and the signing job are unchanged. This replaces #817.

## What we learned
- Lists compiled in with `go:embed` must be in place in every job that compiles, including the Docker builds, which compile from the checked-out tree with no Go on the runner: fetch once in a job with Go, pass the files as job outputs, write them in each build. One fetch also keeps every build of a release on the same lists.
- The jobs that compile shipped Go in `release.yml` are the ones that run `license-key.sh --require`; a new embed step belongs next to it.
- A new job's Go module download runs offline unless the job is listed in `tools/ci/tests/test_go_mod_download.py`'s ONLINE: a tool's own HTTP fetch does not need online module mode.
- A local `go build` in a module directory leaves its binary there, and `git add -A` commits it: #817 carried a 9 MB binary in one commit, and merge commits would have kept it in main's history, so the change was rebuilt on a fresh branch (#819) and #817 closed. Before every push, check that no commit of the branch adds a binary or a file over 1 MB (`git diff-tree --numstat` per commit), and build with `go build -o` outside the tree.
- A test that reads a committed placeholder (an empty plugin root) breaks the day the placeholder is filled: give the test its own copy of the placeholder's form.

## Open
- Once the owner commits the plugin root keys, a release fails while the store's CDN is down (re-run once it is back); the go-live list says so.
