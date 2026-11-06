---
schema: 1
kind: pr
pr: 197
title: A listing on a hung NAS share times out
date: 2026-10-21
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
NAS shares can wedge the Mac's NFS/SMB client. A listing on a hung share now times out instead of
hanging its caller (#197).
