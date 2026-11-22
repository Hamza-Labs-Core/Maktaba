---
schema: 1
kind: pr
pr: 1511
title: "The web search page: suggestions, did-you-mean, facets and sort (story 49.10)"
date: 2026-11-22
session: 42d9474f-9a17-4b0a-b569-ee6c71f0aaf5
agent: claude-code
prs: [1497]
---

## What happened
- #1511 builds Epic 49 story 49.10 on story 49.8's API (#1497). The work is in
  `web/src/pages/searchHelp.tsx`, `Search.tsx` and `searchShared.tsx`:
  - a combobox of suggestions that shows each suggestion's kind, where a title opens its
    video;
  - a did-you-mean row above the results;
  - facet chips with counts that narrow the search and combine;
  - the filters in use, each removable, and Clear all;
  - a sort select.
- The URL carries the sort and filters (`sort`, `lib`, `lang`, `spk`, `year`, `dur`).
- maktaba.dev's web app page gained the new Search text and four dark screenshots
  (`web-search`, `web-search-suggest`, English and Arabic). They were made with vite and
  headless Chrome (`playwright-core` with Google Chrome), with `/api/*` answered by made-up
  data.
- One independent review by GLM-5.3, given the code and the story, found no blocker or major.
  It found four minors, all fixed with tests:
  - leaving the box did not close the list, so focus alone reopened a stale one;
  - the highlighted option could be off screen;
  - a cached page was restored without checking that its filters matched the URL's;
  - a year range's start was not zero-padded.

## What we learned
- Facets come with the first page only, and a saved search's pages (`GET
  /api/search/results/{sid}`) carry neither its sort nor its facets. So a client that pages,
  reloads or goes Back by sid has to keep them itself: the web keeps them per sid in session
  storage (`mkt:search:help`), next to the page sizes.
- The request takes one `date` and one `duration_sec` range, so a year and a duration band
  are single choices. Libraries, languages and speakers are lists.
- The server keeps a limited user's own libraries in a saved search's `filters`, so those
  filters cannot be read back as the chips the person chose.
- For the web's screenshots, the crash-report question (`mkt:crash-reports`) must be set as
  well as the usage-data answer, or its card covers the page.

## Keep doing
- Send only what differs from the defaults (no `sort` for relevance, no empty `filters`):
  the existing request-body tests kept passing unchanged.
- Check a review's fix by turning it off: the blur test failed without its fix.

## Open
- `GET /api/search/recent` carries no sort or filters. A recent search shows its own only
  when this browser ran it, and a saved search keeps neither.
- A shared filtered link opened in a new tab shows a library chip by its id until the facets
  arrive. A sid read without a re-run shows no facet panel.
- Story 49.11 (Apple, Android, TV, big screen) has the same features to build on the same API.
