---
schema: 1
kind: pr
pr: 1447
title: "A release is published only when every file its checksums.txt names is attached"
date: 2026-11-19
session: 9aa2417d-57dc-4c99-a4b6-462f5098d816
agent: claude-code
---

## What happened

#1447 (merge ebc968b51) changed `tools/release/release-assets.sh publish`. It now downloads the draft's `checksums.txt` and keeps the release a draft until every file it names is attached. A home that verifies a release never meets a signed name with no file behind it. It came out of #1440's first review, which found the first-party packages could be checksummed and not uploaded.

There were four Kimi K3.1 Pro review rounds. Rounds one to three found byte-level gaps between the shell reader and `releaseverify.ParseChecksums`: NUL, CR, blank lines, `*name` lines, the scanner limit. Each fix found another. Round three's answer changed approach: the gate accepts only what `release.yml`'s `sha256sum` writes, a strict subset of what the Go parser reads, and anything else keeps the draft with a notice. Round four found nothing.

Tests are in `tools/release/release-assets-test.sh` (a `CHECKSUMS_FILE` fixture for NUL bytes) and `gh-retry-test.sh`. `release-assets-test.sh` now also runs on the macOS installer-tests leg, so BSD tools are covered; `gh-retry-test.sh` stays Linux-only.

## What we learned

- Mirroring a parser in shell byte for byte does not converge. Accepting only the producer's exact format, a subset of the consumer's, does, and its failure is the safe one: a draft waits, and a person sees why.
- Bash 3.2 (macOS) cannot `source <(...)`, and the repo lint refuses `| grep -q` (`test_no_head_pipes`). Use a temporary file, and `[ -z "$(...)" ]`.
- A NUL cannot ride in an environment variable, so a test that needs one passes a fixture file.

## Keep doing

- When a review keeps finding parity gaps, narrow what is accepted rather than widening the mirror.
