---
schema: 1
kind: pr
pr: 1389
title: "Broadcast watch page: a Report button and a pinned, hash-checked hls.js"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1389 merged as b15b9c33d. The watch page now has a Report button: a viewer with no account
  can report a broadcast (copyright, illegal content, abuse, other) in English or Arabic,
  posting straight to the cloud's `POST /v1/moderation/reports` with Turnstile. Sending it
  through the edge would have shown the cloud the Worker's address and put every viewer in one
  rate-limit bucket. The API answers CORS for watch.maktaba.tv on that route only, with no
  credentials. Illegal-content reports take the existing urgent path.
- hls.js 1.5.20 is loaded from jsDelivr with an SRI hash, and the CSP names that one file;
  `test/hls.test.ts` fails if the hash, version or CSP drifts. Self-hosting was rejected: the
  edge has no asset build step and it would add about 400 KB to every Worker bundle.
- Fixed on the way: reports and notices from real watch URLs (`/<lang>/<server id>/<edge
  channel>`) were stored with no channel and never reached the publisher; description lengths
  were counted in bytes, refusing full-length Arabic text the form allowed.
- The review fixed two more:
  - a leading `ar`/`en` was stripped from any URL, so a home slugged `ar` could have reports
    land on another publisher. The language is now read only before a server id, and
    `Resolve` matches ids and slugs separately (a slug is at most 30 characters, an id 36);
  - CodeQL's umbrella check failed on a response-writer wrapper (a reflected-XSS false
    positive). The wrapper was replaced by marking the request so the general CORS layer adds
    no credentials, which also covers a 500 from the outer recover middleware.

## What we learned
- CodeQL treats any custom `http.ResponseWriter` wrapper's `Write` as an XSS sink for every
  handler behind it; prefer request-context flags over writer wrappers in middleware.
- A worktree child started from the parent's checked-out trail branch; it rebuilt its branch
  from origin/main. Start children from a checkout on main.

## Open
- Owner actions: set the edge's `API_ORIGIN` to `https://api.maktaba.tv` (until then the page
  links to the copyright page); the cloud's Turnstile secret must belong to the edge's widget,
  which must list watch.maktaba.tv.
- Reports from the staging edge are refused (the cloud accepts only watch.maktaba.tv).
