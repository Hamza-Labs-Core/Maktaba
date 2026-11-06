---
schema: 1
kind: pr
pr: 539
title: RouteStub redirect race
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Folded into #539. `Credentials.aRedirectToAnotherOriginDropsTheToken` sometimes ended as `.status(302)`.
- The stub sent both `wasRedirectedTo` and the 3xx answer, and URLSession raced them, so a followed redirect sometimes ended as the 302 (2 in 2000 under CPU load). It was not shared stub state; those suites already ran one at a time.
- The stub now asks the request's delegate (`RedirectRule` in `Redirects.swift`) and tells URLSession only one of the two. New test `aRedirectEndsOneWayEveryTime`. Branch `fix/routestub-isolation` @ 23cf5b80e.

## What we learned
- A URLProtocol stub must report either a redirect or the 3xx response, never both; URLSession races them.
