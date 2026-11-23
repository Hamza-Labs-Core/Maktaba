---
schema: 1
kind: pr
pr: 1533
title: "AI mode's tab in the web app, the big screen and /tv/ (story 49.14, web part)"
date: 2026-11-22
session: 8f397c51-c5f8-4528-bb4c-1bc3c729a955
agent: claude-code
prs: [1525]
---

## What happened
- #1533 builds the web part of Epic 49 story 49.14 on story 49.12's `POST /api/search/ai`
  (#1525). Three other sessions build the Apple, Kotlin and Roku parts in parallel.
- The Search page has two tabs, Results and AI mode, with the tab in the URL (`tab=ai`). The
  big screen's Search has the same tabs, so `/tv/` has them too.
- One client serves both, `web/src/lib/searchAI.ts`:
  - it reads the stream from `fetch`'s body, with an SSE reader that gives the same events
    whatever the pieces;
  - each turn is kept by a pure reducer, `applyAIEvent`;
  - the `history` for a follow-up comes from `historyOf`;
  - two hooks: `useAIConversation` (one live turn, aborted when the person asks again, stops
    it or leaves) and `useAITab` (opening the tab asks the words typed; leaving it stops the
    answer).
- maktaba.dev's web app and big screen entries describe AI mode, with four screenshots on
  made-up data. They were made with vite and headless Chrome, with `/api/*` and the SSE
  answered by the script.

## What we learned
- **StrictMode aborted the auto-asked request.** An unmount cleanup that aborts the live
  request also runs in StrictMode's dev rehearsal (cleanup, then setup again at once). It
  aborted the request the tab had just sent, and because the tab asks only once, the turn
  stayed on "Finding passages…". The unit tests did not show it; the screenshot run, on the
  dev server, did.
  - Fix: the abort waits a tick and runs only if the component is still unmounted.
  - A StrictMode `renderHook` test now pins it; it fails without the fix.
- **A fake body never ends on abort.** A test's `ReadableStream` that knows nothing of the
  request's signal kept `reader.read()` waiting after the abort.
  - Fix: the client cancels the reader on abort itself, whatever `fetch` does with the
    signal.
- **The big screen's Enter is the browser's click.** BigScreen leaves Enter from a keyboard
  to the browser's own click on the focused button, so a jsdom `keyDown` Enter does nothing.
  The unit tests focus and click; the `/tv/` Playwright test presses the real keys.
- **The design system's `Tabs` unmounts the hidden panel.** So the conversation lives in the
  page (`useAIConversation` in `Search`), not in the panel, and survives a tab switch.
- **Searches answering late kept dropping the tab.** `showUrl` rebuilt the URL from the
  search alone, so a Results search that answered after the person moved to AI mode put them
  back on Results. It now keeps `tab`.

## Keep doing
- Take the screenshots on the dev server: it runs StrictMode, which the unit tests (without
  StrictMode) do not.
- Pin each fix with a test that fails without it.

## Open
- The `/tv/` Playwright test (49.14) was not run locally, only in CI.
- The CHANGELOG entries and the spec's As built for 49.14 wait for the closing PR, after the
  Apple, Kotlin and Roku parts merge.
