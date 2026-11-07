---
schema: 1
kind: pr
pr: 783
title: The admin orders plugin panels (Epic 54, story 54.20 §1)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
issue: 741
---

## What happened
- Pages show plugin panels in the admin's order (`plugins.panel_order`, Settings → Plugins → Panel order), then install order; one item of issue #741.

## What we learned
- An order kept as a list of ids, with the rest after it in install order, needs no row per plugin and no migration. It also keeps a plugin's place through an uninstall and a reinstall.
