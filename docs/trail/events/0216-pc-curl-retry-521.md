---
schema: 1
kind: pr
pr: 216
title: Linux payload downloads retry a host that is down
date: 2026-10-21
session: 454c33cb
source: claude-trail.md
---

## What happened
An ffmpeg host outage (a Cloudflare 521) failed the Linux payload downloads. #216 made them
retry a host that is down and come from main's cache.

## What we learned
- curl's `--retry` does not retry a Cloudflare 521.
