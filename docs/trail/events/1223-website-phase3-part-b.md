---
schema: 1
kind: pr
pr: 1223
title: "maktaba.tv Phase 3: the problem, legal, chooser and 404 pages in part B's shell"
date: 2026-11-13
session: 1993b809-a720-405a-a7f1-3f67f1dd1d95
agent: claude-code
---

## What happened
- #1223 merged as 55ca91337. Epic 60's Phase 3 moved the problem pages and their index (60.06),
  the legal pages (60.07), the install chooser and the 404 onto part B's shell, as the
  `problem-p3`, `problems-p3` and `legal-p3` mockups draw them. `renderProblem`, `renderIndex`
  and `renderLegal` are unchanged: only `page({ variant: "b", links: bLinks(resolves) })` changed.
  The chooser and the 404 have no part B mockup, so each keeps its part A layout in part B's
  shell. `design/review-phase-3.md` records that choice for the lead.
- The #1219 trap came up again. A family built before `/download/` would have linked GitHub's
  release from its nav, so `FAMILIES` became `[DOWNLOAD, PROBLEMS]`, and a build test checks
  every moved page's nav in both languages.
- Every moved page has part B's social card, with a foot line per kind (`cardSub`,
  `CARD_FAMILIES` in `cards.mjs`). The install guides keep the first template.
- The legal contents list marks the section being read with CSS alone: `scroll-target-group:
  auto` with `:target-current` in `src/part-b.css`, and no script. It changes only a colour and
  a border `.toc a` already reserves, so nothing moves.
- The 404's head now fills `<main>`. Part B's shorter display title had left a strip of the
  page's colour above the footer when the page was shorter than the window.
- This event was written by the session building 60.12 (#1225), which found #1223 merged with
  no event while bringing main into its branch.

## What we learned
- Every family whose pages carry part B's nav has to come after `/download/` is built, or has
  to count it as built. #1225 (60.12, open when this was written) lets a family declare
  `paths` it always builds (`withDeclared` in `build.mjs`), so help, which must go first, links
  `/download/` too, and orders the families `[HELP, DOWNLOAD, PROBLEMS]`.
- `.toc` is shared: the CSS-only current-section mark applies to every contents list in part
  B's shell, the help topics' included, with no change of theirs.
- Two pull requests on the same epic each created `design/review-phase-3.md`; the merge kept
  #1223's file and added the help pages as their own section.

## Keep doing
- Keep a page's content in one render function, apart from the shell, so a shell move changes
  one call.

## Stop doing
- Creating an epic's shared review file from two branches at once without one of them knowing:
  start it on main, or name the other branch's section in it.

## Open
- The lead's signature on `design/review-phase-3.md`, and the owner's read of the Arabic copy
  (Q2, A5).
- The install guides are still in part A's shell until their own part B layout.
