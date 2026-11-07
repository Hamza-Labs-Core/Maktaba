---
schema: 1
kind: pr
pr: 899
title: A nightly run checks the hosts yt-dlp contacts for the YouTube plugin (54.33 T02)
date: 2026-11-07
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [844]
---

## What happened
- Issue 805: `tv.maktaba.youtube`'s `net` runner opens the network as a whole, so nothing compared its reviewed `[trusted] net` list with what yt-dlp reaches.
- `tools/ci/youtube_hosts.py` runs the stages' own command lines (`listing_argv`, `download_argv`) through a recording proxy that notes each `CONNECT` host. A nightly workflow fails on a host the list lacks.
- Stacked on the merge for PR 844; it retargets to main after that lands.

## What we learned
- A real local run reached only `www.youtube.com` for the listing; the download hit YouTube's bot check. A data-centre runner may get that most nights, so a bot wall is a warning, not a failure. Hosts reached before it still fail the night.
- yt-dlp now warns that YouTube extraction without a JS runtime (deno by default) is deprecated. The plugin's `exec` list has no runtime: issue 900.

## Open
- Making the check conclusive may need a self-hosted runner or cookies.
- Issue 900: deno in the bundle and the plugin's `exec` list, or degraded extraction until then.
