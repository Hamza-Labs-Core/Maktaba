---
schema: 1
kind: pr
pr: 127
title: Drop the committed api.exe; tests bind 127.0.0.1
date: 2026-10-19
session: 89e92223
source: claude-trail.md
prs: [119]
---

## What happened
A built `api.exe` landed in the repo with #119. #127 removed it and kept tests off the LAN, so
Windows no longer shows firewall prompts when tests run.

## Keep doing
- Tests bind 127.0.0.1, never all interfaces.

## Stop doing
- Never commit build output.
