---
schema: 1
kind: pr
pr: 1529
title: "AI mode's tab in the Apple Player and on Apple TV (49.14, Apple part)"
date: 2026-11-22
session: 396db35a-4bf0-467a-846c-e6f8a87a9500
agent: claude-code
prs: [1529]
---

## What happened
- #1529 adds Search's second tab, **AI mode**, to the Apple Player (iPhone, iPad, Mac) and
  the Apple TV, against 49.12's `POST /api/search/ai` (#1525). Both apps share MaktabaKit's
  `AIMode.swift`: `SSEParser`, `AIEvent`, `APIClient.askAI` (over `URLSession.bytes(for:)`),
  `AIModeModel` (the conversation, follow-ups with the last three answered turns, Ask again,
  cancel) and `AIWords` (English and Arabic).
- The Player's tab is `AIModePane` and the TV's is `TVAIModePane`. On the TV, the question is
  typed in the search field and asked from **Ask “…”** or **Ask as a follow-up: “…”** under the
  keyboard. A TV has no second text field for follow-ups.
- `MockHome.stubStream` sends a stream's first part at once and each later part on a
  `release()`. The tests use this latch to see the place in line, then the sources before any
  sentence, without racing a timer.
- The CHANGELOG, the spec's As built and the epic status are left to the lead's closing PR for
  49.14 (four parts built in parallel).

## What we learned
- `URLSession.AsyncBytes.lines` drops empty lines. Server-Sent Events end each event with an
  empty line, so the stream is read byte by byte (`SSEParser`). A line is decoded as UTF-8
  only once it is whole. The unit test cuts the stream at every byte, inside Arabic letters
  too.
- A refusal (409, 429, 404) arrives on the same `bytes(for:)` call as a stream. The client reads
  up to 4 KB of the body and throws `APIError.status`, so the problem's `code` and `help` are
  read the way the other routes read them.
- A 404 on `/api/search/ai` means a home from before 49.12 (MockHome's default 404 shows it in
  `testAIModeOnAnOlderHomeSaysItHasNoneYet`). 405 is read the same way, in case a catch-all
  answers a POST.
- A Player view with a fixed width and `lineLimit` risks the accessibility audit's clipped-text
  check at large text sizes. The source cards wrap their text instead.

## Keep doing
- Keep every word in MaktabaKit's catalog when the Player and the TV share a feature. One
  test checks both languages, and neither app's catalog changes.

## Open
- No screenshots of AI mode on maktaba.dev yet. The UI tests attach them
  (`player-*-search-ai-mode*`, `tv-search-ai-mode-*`); the Apps pages can take them from a
  green run.
- AI mode sends no `filters`. The Results tab's filters do not narrow the passages.
- On the Player, a citation's push leaves Search. If the answer is still being written, it
  stops; back on the tab, **Ask again** asks it again.
