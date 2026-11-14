---
schema: 1
kind: pr
pr: 1263
title: "The home's broadcaster and Broadcast page (story 50.22)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1263 merged as e6ffab479. A home admin sets a Live TV channel up for broadcasting (link
  name, invite or open, always on or on demand, qualities, countries) and publishes it after
  confirming its rights. Publish checks run in order (plan, agreement, publishable, uplink,
  encoder, rights) and name the first failure. One ffmpeg per channel builds the ladder (copied
  source plus hardware-encoded rungs) and uploads each segment once to the edge; an offline slate
  in English and Arabic covers absences. Only admins set up, publish or unpublish; the API
  enforces it. Home migration 0227; SchemaRev raised in all three places.

## What we learned
- Home goose migrations run with no allow-missing, so a stack sharing the 02NN range must merge
  in number order; #1336 took 0226 and the stack renumbered to 0227 (#1263), 0228 (#1274),
  0229 (#1284). #1270 also carries 0227 through #1263, so it is stacked on #1263, not beside it.
- Two stories (50.22, 50.26) registered the same cloudlink prefix; Go's ServeMux panics on a
  duplicate pattern, and only the merge of both showed it. One `cloudlink.BroadcastLoopback`
  now dispatches to whichever feature lists the path.
- A new rules section needs its help anchor and Arabic in the same push; the web coverage tests
  (`help.coverage`, `localizeRules`) fail without them, and every stacked branch inherits it.
- Removing conflict markers by deleting marker lines is not a resolution: on two branches it
  dropped main's CHANGELOG entries. After any CHANGELOG fix, check
  `git diff origin/main...HEAD -- CHANGELOG.md CHANGELOG.ar.md | grep '^-[^-]'` prints nothing.

## Open
- The edge does not send wake/sleep/limits yet, so on-demand channels run always on.
- A home-side analytics page on the Broadcast page (50.28's data exists).
