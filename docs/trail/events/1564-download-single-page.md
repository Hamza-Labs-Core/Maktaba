---
schema: 1
kind: pr
pr: 1564
title: "One page to get Maktaba: /download/ lists every setup guide, /install/ redirects to it"
date: 2026-11-24
session: ef3aedc5-a524-4481-9ef0-49054edba1df
agent: claude-code
prs: [1564]
---

## What happened
- maktaba.tv had two overlapping pages: `/download/` (60.11, the release's files per device)
  and `/install/` (60.05, the guides' chooser). The decision was to keep one page, Download.
- #1564 adds the guides to the download page under its cards (`#guides`, with `#nas-guides`,
  `#docker-guide` and `#after-guides`), using the chooser's wording. `/install/` and `/ar/install/`
  now return a 301 to `/download/` and `/ar/download/` through the Worker's table
  (`src/redirects.json`), the same way as `/docs`. Every `/install/<guide>/` address stays
  as it was.
- Install leaves the nav and footer. Each guide's "All NAS guides" link goes to
  `/download/#nas-guides`. The deploy smoke checked `/install/` for a 200 and now checks the
  page and both redirects.
- The nav change touches every page, so all 176 visual references were deleted, and CI's image
  rendered the 168 that remain (the chooser's 8 are gone; Docker is not used locally). Every
  reference comes from shard 4 (`website-new-references-4`): shards 1 to 3 hold no visual tests.
- The first render showed the guides' groups touching: `gap: var(--spacing-10) …` named a token
  the scale does not have (it goes 8, 12), so the whole declaration was dropped. #1564 uses 12
  and 8.

## What we learned
- The Worker checks the redirect table before the assets, so a redirect can replace a page that
  still has a source. Removing the source as well keeps the sitemap, the twin check and the
  social cards from listing a page nobody reaches.
- A copy change that a mockup reads (`meta.description`) makes `make test-tokens` fail as a
  stale mockup. Keep the copy keys the mockups read unchanged in a layout change, or regenerate
  and render the mockups.
- The wiki database stores a story's text truncated, so text appended to the end of a story
  does not change its Arabic hash.

- An undefined custom property in a shorthand drops the whole declaration silently: no lint,
  no test caught `--spacing-10`, only looking at the rendered reference.

## Keep doing
- Grep for the bare index URL (`maktaba.tv/install/` with nothing after it), not only for
  `/install/`: the guide URLs are everywhere and must not change.
- Check every link-shaped test (smoke, link check, build tests) when a page becomes a redirect.

## Stop doing
- Keeping a second index page for content one page already reaches. Two entry points drifted
  in their wording and nav.

## Open
- Part B's `design/site.css` and `src/download.css` use `--spacing-10` about ten times (the
  section head's margin, the wide card's column gap), so those spaces are 0 on the live site
  today. A CSS check that every `var(--…)` names a defined token would catch it.
- The guides section's tile is a new part B pattern, proposed in #1564 with screenshots and
  waiting for design review. The approved download mockup does not show it yet.
