---
schema: 1
kind: pr
pr: 1697
title: "Search knows a URL Back left before it rendered; a failed model load tells every file"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
prs: [1683, 1690]
---

## What happened
- The review of #1683 found that a URL the page wrote and Back left before it rendered was still
  taken for the URL: the location's key and params were the ones rendered before. #1697 counts
  each write in the navigation's own transition and takes the rendered URL again with it.
- It also leaves the same words opened in another order alone, tells the files after one whose
  event loop is closed that the model failed to load (#1690's review), and adds the changelog pair
  #1683 left out.

## What we learned
- State set in the same event and transition as a navigation renders with it, so it says when the
  navigation has settled, even where the URL ends up as it was.
- No test needed `urlKey`: main's tests passed with it removed, though the behaviour did need it.
  Remove the line a fix turns on and see a test fail before calling it covered.
