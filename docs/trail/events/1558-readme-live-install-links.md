---
schema: 1
kind: pr
pr: 1558
title: "The README's install guides link to maktaba.tv, not to the guides' HTML source"
date: 2026-11-24
session: 6c776e9f-79b4-4a72-a98b-2044a761ee11
agent: claude-code
prs: [1558, 1549]
---

## What happened
- #1549 shortened the README while maktaba.tv was not live yet. Its NAS and Docker guide links
  pointed at the guides' source, `web/packages/install/site/install/*/index.html`, and a note
  said the site would host them later. On GitHub those links open raw HTML: no use to a
  household.
- maktaba.tv went live (`WEBSITE_LIVE=true`; every sitemap URL answers 200).
- #1558 points every install guide link at maktaba.tv/install and its pages: nas, synology,
  truenas, unraid, qnap, ugreen, asustor, docker, after-setup, troubleshooting. ASUSTOR gets its
  own row. `deploy/nas/*` stays only as "the files" beside each guide.
- The same fix went into `docs/wiki/deployment.md`'s Docker section and
  `deploy/nas/truenas/README.md`, which linked the TrueNAS guide's source files.

## What we learned
- A link to a page's source is not a link to the page. An HTML file in the repository is
  markup on GitHub, not a guide anyone can read.
- `links.mjs` passes a maktaba.tv link that is in `dist-manifest.json`, whether or not the site
  is up. Whether a page answers is a separate check (`curl` each one).

## Keep doing
- Grep the whole repository's Markdown for links into a source tree once its site is live, not
  only the file at hand.

## Stop doing
- Linking a household-facing guide to its HTML source as a stand-in until the site goes live.
  Link the doc that has the same content, or the live page once it exists.

## Open
- The NAS device checklists (`docs/nas/checklists/*.md`) name `web/packages/install/site/screenshots/`
  folders as plain paths, for where to put the screenshots taken on a device. They are paths,
  not links, and stay.
