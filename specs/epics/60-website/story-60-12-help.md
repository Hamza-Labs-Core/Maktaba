# Story 60.12 — Help on maktaba.tv, from the app's own topics

> Epic 60 · Phase 3 · Depends on: 60.01 part B (help layouts), 60.03, 60.04, Epic 41's topics

## Why

Epic 41 wrote help for every feature, 44 topics in English and Arabic, shown inside the app at
`/help/<topic>`. A household that has not installed Maktaba yet cannot open it. Neither can one
whose home is down, which is exactly when help is needed. The desktop app's Help menu already
opens `https://maktaba.tv/docs`. Epic 41 left a public help site out of scope. This story
publishes the same topics, not a second copy, so the two cannot drift.

## Description

**One source.**
- `scripts/help.mjs` reads `web/src/help/content/{en,ar}/*.md` and the topic list
  (`web/src/help/topics.ts`, read as data).
- It renders `/help/<topic>/` and `/ar/help/<topic>/` with the same Markdown features the app's
  `HelpMarkdown` supports: headings with ids, lists, tables, code, callouts.
- A topic missing in Arabic falls back to English in the app. On the site it **fails the build**
  instead: a public page in the wrong language is a defect.

**Links, rewritten at build time:**
- `/help/<topic>#<anchor>` → `/help/<topic>/#<anchor>` (or `/ar/help/…`). The anchor must exist,
  and the link check proves it.
- **App routes** are not pages on the site: 167 links in the English topics, 104 of them to
  `/settings/…` and `/admin/…`, and the rest to `/account`, `/queue`, `/library`, `/videos`,
  `/login`, `/register`, `/pair`, `/cloud`, `/guide`, `/search`, `/series`, `/playlists`,
  `/review`, `/transcription` and others. The rewriter takes the route list from the app's router
  (`web/src/App.tsx`), not a hand-made list.
  - They render as text in a "where" style: "In Maktaba: **Settings → Privacy**" (Arabic: «في
    Maktaba: **الإعدادات ← الخصوصية**»).
  - The label comes from the link's own text.
  - The arrow is a direction mark and mirrors.
  - A link to the app's root (`](/)`) renders as "In Maktaba: the home screen", never as a link
    to the site's home.
  - A test fails an app route the rewriter does not recognise.
- **External links** stay as written.

**Pages:**
- **`/help/`:** the index, grouped as the app groups topics, with a search box that filters in the
  page over titles and headings (no request).
- **A topic:**
  - 60.01's help topic layout: the topic, its table of contents on wide screens, and "Related
    topics";
  - a line at the top: "This is the same help you'll find in the app, under Help."

**Redirects:** `/docs` and `/docs/` become 301 to `/help/` in the Worker (60.03 had them going to
`/`).

**Where the app links.** In this story nothing in the app changes: the app's own help stays the
primary, and works offline. The desktop menu's `DOCS_URL` (`web/src/lib/desktop.ts`) changes from
`https://maktaba.tv/docs` to `https://maktaba.tv/help/`, and the Arabic UI opens `/ar/help/`.

**The quality run** covers representatives on pull requests (the longest topic, the shortest, one
with a table) and every topic on deploy.

## Acceptance criteria

- **Given** each of the 44 topics, **then** `/help/<topic>/` and `/ar/help/<topic>/` exist, render
  the topic's Markdown, and pass 60.04's bar.
- **Given** a topic whose Arabic file is missing, **then** the site build fails naming it.
- **Given** any link in a topic, **then** it resolves on the site, or it renders as an "In
  Maktaba" path.
- **Given** `/docs`, **then** it redirects to `/help/`, and the desktop app's menu opens `/help/`
  (or `/ar/help/`).
- **Given** a change to a help topic in the app, **then** the next deploy publishes it (the path
  filter includes `web/src/help/content/**`).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | build | the help content | `help.mjs` | 88 pages and 2 indexes |
| T02 | build | an English topic without its Arabic file (fixture) | build | fails naming it |
| T03 | unit | `[Privacy](/settings/privacy)` | rewrite | "In Maktaba: Settings → Privacy" markup, en and ar |
| T04 | unit | an app route the rewriter does not know (fixture) | rewrite | fails |
| T05 | link check | built help | 60.08 | every link and anchor resolves |
| T06 | Worker unit | `/docs` | fetch | 301 `/help/` |
| T07 | vitest | `desktop.ts` | Help menu in en, ar | `/help/`, `/ar/help/` |
| T08 | Playwright | `/help/` | type in the filter | the list narrows; no request |

## Strings

| Key | English | Arabic |
|---|---|---|
| `help.title` | Help | المساعدة |
| `help.sameAsApp` | This is the same help you'll find in the app, under Help. | هذه هي المساعدة نفسها التي تجدها في التطبيق، في قسم المساعدة. |
| `help.where` | In Maktaba: | في Maktaba: |
| `help.related` | Related topics | مواضيع ذات صلة |
| `help.filter` | Find a topic | ابحث عن موضوع |

## Edge cases

- **A topic that describes an admin-only page:** published like every topic. Help explains; it
  grants nothing, and the API enforces roles.
- **Help images** (if a topic gains one): copied with the topic and checked by 60.09's metadata and
  size rules.
- **A topic id that changes:** the old path gets a 301 in the Worker's redirect table, kept with the
  topic list.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/scripts/help.mjs`, tests | render and rewrite |
| `web/packages/website/src/pages/help/` templates | index and topic |
| `web/packages/website/src/worker.ts` | `/docs` → `/help/`; renamed topics |
| `web/src/lib/desktop.ts`, its test | `DOCS_URL` |
| `.github/ci-paths.yml` | `web/src/help/content/**` in the `website` filter (60.03 lists it) |
