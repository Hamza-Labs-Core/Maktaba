# maktaba-cloud-web (app.maktaba.tv)

The user app's bundle (`web/dist-cloud/`) behind a Cloudflare Worker (Epic 50 story 50.32 §3).
`run_worker_first` sends every request through `src/worker.ts`:

| Path                                | Answer                                                                      |
| ----------------------------------- | --------------------------------------------------------------------------- |
| `/v1/admin/*`, `/v1/staff/*`        | 404 JSON from the Worker: the console's API is never reached from this host |
| any other `/v1/*`, `/.well-known/*` | passed to the origin unchanged (`fetch(request)` through the Tunnel)        |
| anything else                       | the bundle; an app route without its own file gets `index.html`             |

Every response carries the host's headers (§5): its CSP, HSTS, `nosniff`, `X-Frame-Options:
DENY`. The rules are in `src/edge.ts` (shared with `admin-web`) and `src/site.ts`; the tests
(`src/*.test.ts`) run in `web/`'s vitest.

It is attached by a **Workers Route** on the proxied name, never a custom domain: the name's DNS
record is the Tunnel's CNAME (story 50.31). Story 50.34 deploys it from CI.
