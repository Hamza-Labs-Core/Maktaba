---
schema: 1
kind: pr
pr: 820
title: The plugin store's isolated runner on Cloudflare Containers (Epic 54, #756)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [770]
---

## What happened
- A `CheckRunner` Durable Object runs the store's isolated checks (C7 ClamAV, C8 the sandbox run, C10 the reproducible build) in a Cloudflare Container per submission, started with its internet off and handed the package and source over its local port; `check-runner/serve.py` is the container's server. `RUNNER = "containers"` turns it on; the GitHub Actions runner stays the default.
- The containers deploy config is generated (`scripts/containers-config.mjs`) and used only when the repository variable `STORE_RUNNER` is `containers`, so deploys keep working on accounts without Containers.

## What we learned
- A Durable Object's container is `ctx.container` (start with `enableInternet: false`, talk to it through `getTcpPort`); no library is needed. Miniflare runs no containers: test the store's side with a stand-in namespace passed in the env, and the container's server as a real process.
- Large inputs go to the container from storage inside the Durable Object, streamed over its port: RPC arguments are limited in size and the container has no network to fetch them.
- A `containers` block in the committed wrangler config would make every deploy need Containers on the account; generate it at deploy time behind a repository variable instead.
- A test that expects a forged `Content-Length` must send it with `node:http`; `fetch` refuses, and a `.catch` that turns the error into the expected status hides that.

## Open
- A real container run waits for the owner to turn Containers on (store README).
