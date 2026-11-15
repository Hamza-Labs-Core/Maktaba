---
schema: 1
kind: pr
pr: 1277
title: "Epic 58 story 58.09, part 1: the Samsung and LG launcher"
date: 2026-11-14
session: 9b51ec8c-51c3-4a1a-907e-12b713223f17
agent: claude-code
---

## What happened
- #1277 merged as ee9dfad96. `apps/tv/web` is the Samsung (Tizen) and LG (webOS) launcher. It
  finds the home (the saved home, a probe of the TV's /24, or a typed address), checks the API
  levels with 58.01's vectors run in TypeScript, and loads the home's `/tv/` in a full-screen
  iframe. It talks to `/tv/` over a bridge at `bridge_level` 1 (`apps/tv/web/bridge.schema.json`,
  validated by both sides).
- In the launcher, `/tv/` signs in by the home's own pairing code and keeps bearer tokens in the
  home's origin (`web/src/tv/session.ts`). `lib/api.ts` gained a `BearerAuth` hook: an
  Authorization header, no cookie, no CSRF, and one refresh-and-retry on a 401.
- The home serves `/tv/` with `frame-ancestors 'self' file: app:`, answers
  `/api/discovery/home` with `*`, and echoes a packaged app's origin (`null`, `file://`,
  `app://…`) without credentials.
- `pnpm build:launcher` and `build:launcher:bundled` (`--bundle-ui`) both emit one classic
  deferred script, checked for Chromium 68. `pnpm e2e:launcher` drives the launcher from
  `file://` with Tizen and webOS shims against a mock home, in English and Arabic.

## What we learned
- Chromium never sends a SameSite=Lax session cookie to an iframe inside a `file://` page: in a
  packaged TV launcher, the framed UI cannot use the web sign-in, and needs a code sign-in with
  bearer tokens.
- A module script from a `file://` page is a cross-origin fetch with origin `null`: packaged
  pages need an IIFE loaded with a classic `defer` script.
- Samsung's web runtime user agent has no `Chrome/` token (`69.0.3497.106.1/5.5 TV`): reading
  only `Chrome/N` gives 0 on a real Samsung TV.
- A second `Content-Security-Policy` header intersects with the first: loosening
  `frame-ancestors` means rewriting that directive, not adding a header.
- Playwright tries the newest route first, and `route.fallback()` goes to older ones: a
  catch-all registered last swallows every fallback.
- A background security review caught a bearer token that a protocol-relative or `/\t/` picture
  URL would have sent to another host. Parse the URL and compare origins; never prefix-match.

## Keep doing
- Gating a framed UI's outbound postMessage on a verified hello when the parent's origin is
  opaque and `*` is the only target.
- Building every image that copies part of the repo locally (`docker build --target <stage>`)
  when a shared tsconfig or import reaches outside its directory.

## Stop doing
- Treating a path that starts with `/` as same-origin before sending credentials.
- Deciding CORS from the presence of an Authorization header.

## Open
- Part 2, after #1240: the cloud code as the default sign-in, `signIn {grant}` on the bridge,
  and CORS on the cloud's `/v1/tv/*`. It has since merged as #1348.
- On a real TV: the origin Tizen and webOS report for the packaged page, and whether they honour
  `frame-ancestors file: app:`.
