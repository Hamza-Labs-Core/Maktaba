# The broadcast edge

Epic 50 story 50.21: the multi-tenant Cloudflare Worker a published channel plays from.
Homes push each HLS segment once to `ingest.maktaba.tv`; players fetch playlists and segments
from `live.maktaba.tv` at the edge. Infrastructure only: it never chooses, ranks or recommends
content.

- `src/worker.ts`: the hosts; `src/ingest.ts`: the home's side; `src/live.ts`: the players'
- `src/channel_live.ts`, `src/publisher.ts`, `src/revocations.ts`: the Durable Objects
- `src/playlist.ts`: the timeline and the HLS playlists; `src/tokens.ts`: the credentials
- `src/stream_fallback.ts`: the Stream fallback (Epic 34's signer)

```sh
npm ci && npm test              # typecheck, then the tests in Miniflare
node scripts/rehearsal.mjs      # wrangler dev, a fake home and simulated viewers
```

How it works, its settings and the owner's steps before a deploy: `docs/broadcast-edge.md`.
