---
schema: 1
kind: pr
pr: 835
title: The store browser draws a plugin's flow; the store draws with the shared chart (Epic 54, 54.18, 54.41)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [770, 815]
---

## What happened
- The signed catalog carries each plugin's approved flow (graph, published events, subscriptions) in the entry's `extra`; the home's Browse draws it with `PluginFlowChart` and lists the plugin's events and subscriptions.
- The store's review and listing pages draw with the plugin SDK's `renderSvg` instead of the store's own layout.

## What we learned
- A new field in a signed list goes where old readers keep it unchecked (the catalog entry's `extra`, a raw JSON field in Go, Rust and TypeScript), not as a new top-level key that strict decoders refuse.
- Check for a shared implementation before drawing or checking something twice: the SDK's chart (54.41) landed while the store's own was in review; one layout means the listing, review and the home show the same picture.

## Keep doing
- Stack follow-ups that touch the same files as an open PR on its branch (merged into a branch from main), and open each when the one below merges.

## Open
- #757 and #758 follow on this branch.
