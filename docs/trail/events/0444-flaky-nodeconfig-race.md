---
schema: 1
kind: pr
pr: 444
title: Flaky - a node config write between two looks
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [359]
---

## What happened
A node config write that ends between two looks was read as "never joined". Fixed for
`node.json` in the server config (#359) and in streaming (#444).
