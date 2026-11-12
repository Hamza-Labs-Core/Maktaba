---
schema: 1
kind: correction
pr: 1200
title: "maktaba.tv's token check no longer requires DNS Write: Cloudflare creates a custom domain's record itself"
date: 2026-11-12
session: 674cb245-62a2-49a9-b9ef-f92fd76e49d3
agent: claude-code
corrects: 1183-website-shared-token
prs: [1183]
---

## What happened
- After #1183, the maktaba.tv deploy's token check (run 43148096477) reported `MISSING DNS Write`
  for the shared `CLOUDFLARE_API_TOKEN`. #1183's event recorded this as an open owner action:
  add DNS Edit on `maktaba.tv`. That was a false alarm.
- A Worker's custom domains need no DNS Edit: Cloudflare creates the custom domain's DNS record
  and certificate itself. maktaba.dev already shows this. It attaches `maktaba.dev` and
  `www.maktaba.dev` as custom domains with the same shared token, its deploys succeed, and
  nothing was ever added to the token for it. On `maktaba.tv` the shared token passes the
  Workers Scripts, Workers Routes, Zone Read and Zone Settings probes; it fails only the DNS and
  Zone WAF probes.
- #1200: the website deploy checks `--pieces workers` only, and `token-check.mjs` no longer has
  the `dns` piece (DNS Write stays in `tunnel`, which does write records). The token table, the
  wrangler comments, the READMEs, the website guide (en, ar) and the Epic 34/50/60 specs no
  longer say custom domains need DNS Edit. The tests pin a token without DNS Write passing.

## What we learned
- Before requiring a permission, check whether a working deploy already does the same thing
  without it. maktaba.dev was the proof that DNS Edit wasn't needed.
- A probe failure names what the token cannot read, not what the deploy needs; the piece table
  is where a wrong requirement hides.

## Open
- The Zone WAF Write that `cloudflare-zone.yml` reports missing is real; it is unchanged here.
- Go-live (`WEBSITE_LIVE = true`) stays the owner's call.
