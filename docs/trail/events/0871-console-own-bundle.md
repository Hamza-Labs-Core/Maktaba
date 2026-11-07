---
schema: 1
kind: pr
pr: 871
title: The operator console is its own bundle (story 50.32, part 1)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [865, 867]
---

## What happened
- 50.32 §2: the console moved to `web/admin` (its own Vite root, `dist-admin/`), unchanged inside, with its routes at the root of its host and its `admin.*` strings.
- Shared code went to `web/cloud/src/shared`: the API client, session, i18n provider and ui.
- The user app has no admin route or link, and an unknown path is now a 404 page.
- `tools/lint/no-admin-in-user-bundle.mjs` checks the real user build in the web unit job, and ESLint forbids the import.
- Each `index.html` carries `maktaba-app` for 50.34's smoke check (#867).
- #865 (the relay-plans rehearsal) merged on green.

## What we learned
- A shared i18n provider must take its catalogs from the app that mounts it. Importing them inside the provider is how one bundle ships the other's strings.
- The source map's `sourcesContent` carries comments, so the check reads comments too. A user-side comment that names an admin API path, or quotes an admin key, would fail the build. Keep such names out of `web/cloud` comments.
- Run the check against the admin build once as well: it should name every console module, which shows the check can fail.

## Keep doing
- Prove a lint rule fires before relying on it. A throwaway file importing `web/admin` from `web/cloud` showed the ESLint pattern catches it.

## Open
- 50.32 §1 (two listeners, `server.admin_listen_addr` from 50.31), §3 (the Workers), §4 (`__Host-` cookies) and §5 (CSP per host): after 50.31 lands.
- `dev:admin` points at :8091, which serves nothing until §1. Use `CLOUD_ADMIN_API_URL=http://localhost:8090` until then.
