---
schema: 1
kind: pr
pr: 836
title: Open in my Maktaba without an app, at the home's own address (Epic 54, 54.18, #757)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [815, 835]
---

## What happened
- A plugin's listing page asks for the home's address where no Maktaba app opens `maktaba://`, keeps its origin in that browser only and opens `<home>/settings/plugins?store=<id>`; the home's Settings → Plugins opens Browse on the plugin.

## What we learned
- A page with `default-src 'none'` can run one inline script by allowing exactly its sha256: keep the script fixed (data goes in the HTML's attributes), compute the hash from the same constant the page writes, and test that the header names it.
- A module the console's jsdom tests import must not import the Worker's own types (`env.ts` pulls in the Durable Objects): keep such a script in a module of its own.
- In the store's jsdom project Node's own `localStorage` global shadows jsdom's: stub an in-memory one.
- `fetch` will not send a forged `Content-Length`; test a length check with `node:http`.

## Open
- None.
