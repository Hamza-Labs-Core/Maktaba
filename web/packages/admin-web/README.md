# maktaba-admin-web (admin.maktaba.tv)

The operator console's bundle (`web/dist-admin/`) behind a Cloudflare Worker (Epic 50 story
50.32 §3). `run_worker_first` sends every request through `src/worker.ts`:

| Path                                     | Answer                                                                             |
| ---------------------------------------- | ---------------------------------------------------------------------------------- |
| `/v1/staff/*`, `/v1/admin/*`, `/healthz` | passed to the origin unchanged (the api role's admin listener, through the Tunnel) |
| any other `/v1/*`                        | 404 JSON from the Worker                                                           |
| `/robots.txt`                            | disallows everything                                                               |
| anything else                            | the bundle; an app route without its own file gets `index.html`                    |

Every response carries the host's headers (§5): its CSP, HSTS, `nosniff`, `X-Frame-Options:
DENY`, `Referrer-Policy: no-referrer`, same-origin COOP and CORP, the Permissions-Policy (passkeys
for this origin only), `X-Robots-Tag: noindex, nofollow`, and `Cache-Control: no-store` on HTML
and `/v1/*`.

It is attached by a **Workers Route** on the proxied name, never a custom domain. Story 50.34
deploys it from CI.
