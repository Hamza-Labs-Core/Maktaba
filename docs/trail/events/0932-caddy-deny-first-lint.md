---
schema: 1
kind: pr
pr: 932
title: A CI check that keeps every Caddyfile from hiding a deny behind its handle blocks
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [927, 930]
---

## What happened
- After #927 and #930, the pattern behind both bugs was made a CI failure everywhere. `tools/ci/caddy_deny_first.py` reads every tracked Caddyfile and snippet. Within one block, `handle` or `handle_path` must never sit beside a bare `respond`, `reverse_proxy`, `file_server`, `abort`, `error` or `php_fastcgi`. `route` blocks are exempt, since their directives run as written.
- It runs in lint's `repo` job, on every pull request but a trail-only one. `test_ci_paths.py` pins that a change to any tracked Caddyfile runs it.
- The two files that broke the rule (origin-aop.caddy before #927, Caddyfile.witness before #930) are its test fixtures, and both fail on the right line. Before #930 merged, the check run on the repo found exactly the witness file.

## What we learned
- A static check of the source can be the better guard than running the tool. `caddy adapt` needs every module a file uses (the pair's Caddyfile needs the Cloudflare DNS module, which stock Caddy lacks) and every environment variable. The rule itself is local to one block, so a small tokenizer covers all nine files with no setup.
- `deny-first.sh` (#930) on the adapted JSON stays as the semantic check for the two files with deny rules. The static check is the broad, cheap one.

- The ci_paths guard passed locally and failed in CI: it reads `git ls-files`, and the new script was not committed yet. Run the guard after committing a new file. Any `tools/ci` script that the lint job runs on every pull request belongs in `NOT_IN_CI`.

## Keep doing
- When a bug's cause is a pattern, add a repo-wide check that keeps the exact files that broke it as fixtures.

## Open
- Nothing.
