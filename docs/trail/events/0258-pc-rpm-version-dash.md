---
schema: 1
kind: pr
pr: 258
title: Linux app bundles for a pre-release tag
date: 2026-10-22
session: 454c33cb
source: claude-trail.md
---

## What happened
v0.3.0-rc.1's Linux release legs failed. #258 fixed bundling the Linux app for a pre-release
tag.

## What we learned
- rpm takes no `-` in a version.
