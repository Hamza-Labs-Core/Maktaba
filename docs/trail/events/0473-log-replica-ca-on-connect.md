---
schema: 1
kind: pr
pr: 473
title: The home pushes its database CA when a machine connects
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Flake fixed at the root (replica e2e): the home pushes its database CA when a machine agent connects or leaves, not only every 30 s.
