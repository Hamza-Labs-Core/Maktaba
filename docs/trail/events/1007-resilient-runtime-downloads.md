---
schema: 1
kind: pr
pr: 1007
title: A home's Postgres download survives github.com's download door failing
date: 2026-11-08
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [944, 907]
---

## What happened
- From about 09:15 UTC on 2026-11-08, github.com's `/releases/download/` answered 504 on the first request, while api.github.com and release-assets.githubusercontent.com kept working and GitHub's status page stayed green.
- A home fetches the Postgres a node's copy runs (`pgbin`) from that door at runtime, so a new node copy or a Postgres upgrade on a real home failed during it.
- #1007 (merged at 5a197735d) makes `pgbin` try three places in order:
  - Maktaba's CDN (`plugins-cdn.maktaba.tv/postgres/<version>/<archive>`, which `postgres-cdn.yml` publishes after each release, checking each archive against `tools/release/postgres-cdn.txt`, which a test keeps equal to the pins);
  - GitHub's API asset route, looked up by tag and name and fetched with `Accept: application/octet-stream`, with no credentials;
  - the github.com door.
- Each place gets three tries with backoff on a 5xx, a timeout, a connection error or a stalled body. Whatever arrives must match the pinned SHA-256 and size in a hidden temp file, which is deleted unless it matches. The log names the place that delivered.
- Packs got retries and the API fallback for Maktaba's own repository, and are still checked against the signed index.
- The updater, self-update, the update catalog and packs inside an update job were left alone: they are update paths and need an update-rehearsal T-row first.
- A child session built it. The parent added the ci-paths entry for the new workflow and a `ProgramsFor` test that the affected-tests gate asked for, and removed a stray root `uv.lock` its own local `uv run` had created and `git add -A` committed.

## What we learned
- github.com's release download door can fail on its own. The API asset route reaches the same file on release-assets without it.
- The same outage reached CI. The Windows desktop-player job's `:downloadWix` fetches WiX from a github.com release and timed out, as did the Gradle wrapper from services.gradle.org.
- The store CDN (`plugins-cdn.maktaba.tv`) does not resolve yet. Until the bucket and its domain exist, the publish step warns and skips, and homes skip the CDN on the DNS miss and go to the API route.
- `uv run` in a checkout writes a root `uv.lock`, and `git add -A` commits it. ci-paths then fails the lint job on an unlisted file.
- A merge watcher started right after a push reads the previous head's checks and stops on their failures. One run from outside the repository can't read checks at all.

## Keep doing
- Check every downloaded archive against its pin, whichever place sent it.
- Start a merge watcher from the repository, a couple of minutes after the push.

## Stop doing
- `git add -A` after running tools that write into the checkout. Stage the files by name.

## Open
- Move the updater and the update catalog to the API route, with an update-rehearsal T-row.
- Create the CDN bucket (`maktaba-plugins`) and its domain, and give the release token write access to it (an owner decision).
- CI's WiX download could get the same fallback, or be cached.
