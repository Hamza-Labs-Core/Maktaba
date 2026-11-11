# Architecture overview

This page is a short map. The canonical design, with data flow and the reasons behind
each choice, is
[specs/architecture.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/architecture.md).

## The services

A Maktaba home runs four services that share one Postgres database for state and one
media volume for the video files. Apps and the optional cloud talk to them.

| Service | Language | Role |
|---|---|---|
| API | Go | REST, GraphQL and WebSocket. Auth and roles, libraries, search orchestration, job control, settings, watch state. Signs stream URLs. |
| Streaming | Go | HLS and DASH origin. FFmpeg transcode and remux, range serving, subtitles, sprites, and the live TV channels. |
| Pipeline | Python | Scanner, probe, audio extraction, Whisper transcription (MLX, faster-whisper or OpenAI), embeddings, indexing, diarization. Drains the job queue. |
| Web | React, TypeScript, Vite | The web app (a PWA) and the in-app user guide at `/help`. The desktop server apps show it. |
| Native apps | Swift, Kotlin | Maktaba Player (iPhone, iPad, Mac, Android, Windows, Linux), Maktaba Remote (iPhone, iPad, Android) and Maktaba TV (Apple TV, Android TV). |
| Cloud relay (optional) | Go | Hosted layer for remote access: identity, server linking, a WSS-tunnelled HTTP relay and push fan-out. Lives in `cloud/`. |

`maktaba-server` bundles the API, streaming and the web UI into one binary for
installs; the desktop apps (Tauri) carry that server.

## One language per concern

Python does ML, Go does servers, TypeScript does UI. A new feature belongs to the
service that owns the relevant state; it does not spread across runtimes.

## The database

- PostgreSQL 16 with pgvector holds all state: libraries, videos, transcripts, the job
  queue, users and settings. Full-text search (`tsvector`) and semantic search
  (pgvector) run in the same database.
- SQLite is supported for single-user mode. Migrations ship as numbered SQL files
  under `shared/db/migrations/`, with Postgres and SQLite versions kept in step.
- Every service may read; only the owning service writes its own tables.

## How the services talk

- **API to pipeline:** gRPC for query-side work such as embedding a search query.
  Bulk job control goes through Postgres: the API inserts jobs, and pipeline workers
  claim them with `SELECT ... FOR UPDATE SKIP LOCKED`.
- **API to streaming:** gRPC for session lifecycle and capability negotiation. The API
  signs stream URLs; clients fetch bytes straight from streaming over HTTPS, so video
  traffic never passes through the API.
- **Events:** Postgres `LISTEN/NOTIFY` carries "job state changed", "new video ready"
  and similar events. The API turns them into WebSocket frames for clients. There is
  no separate message broker.
- **Files:** the pipeline owns the filesystem watcher. Streaming reads files only when
  a request asks for them. Generated sidecars (subtitles, thumbnails) live in a hidden
  `.maktaba/` folder next to each source file, so a library stays portable.
- **Clients:** the web app and native apps use the API (contracts in `shared/api/`:
  `openapi.yaml` and the GraphQL schema) and play from streaming.
- **Cloud:** a home links to a Maktaba Cloud account and keeps an outbound tunnel to the
  relay, so apps can reach it away from home without opening ports.

### Client addresses

The API and streaming resolve each request's client address once, in their router's
first middleware (`shared/go/clientip`), and rewrite `r.RemoteAddr` to it. A handler, a
rate limiter or a log line reads `r.RemoteAddr` and never reads `X-Forwarded-For` itself:
that header is text the client can write.

The middleware believes `X-Forwarded-For` (else `X-Real-IP`) only from a trusted peer:
loopback, which is this machine's own proxies (the web port's, the relay agent, direct
access), and the reverse proxies listed in server.toml:

```toml
[server]
# a reverse proxy in front of Maktaba (a NAS's, for example): addresses or CIDRs
trusted_proxies = ["198.51.100.5", "203.0.113.0/24"]
```

It walks the header right to left past the trusted hops; the first address that is not
trusted is the client. From any other peer the header is ignored and the peer is the
client. The list is empty by default, is set in Settings → Storage & discovery, and
reaches both roles as `MAKTABA_TRUSTED_PROXIES`; a bad entry, or a `/0` (`0.0.0.0/0`,
`::/0`, which would believe every peer), stops them from starting and is refused by the
settings form. A
check that must know whether the caller is a program on this machine (`MarkLocalCaller`,
`StripVia`, the stream gate's viewer header) runs on the connection's own address, before
the rewrite or through `clientip.Peer`; so do the streaming checks that answer this machine
only (the link-required gate, the TV slots, the offline station), since a chain of trusted
hops resolves to its leftmost address, which a client can write as `127.0.0.1`. A zoned
link-local address in the header (`fe80::1%eth0`) loses its zone.

The home's approved nodes are trusted by signature, never by address. A node's home proxy
(`homeproxy`) puts the client it resolved in `X-Maktaba-Forwarded-Client`, and its link's
signer adds `X-Maktaba-Forwarded-Client-Sig`, an HMAC with the node's delegation key over
that address and the request's own signature (so over its method, path, body, timestamp and
nonce). `delegation.Accept`, which wraps the home's public handler before the router, checks
it after the request signature and puts the client in the context
(`clientip.WithAttested`); the router's middleware uses it in place of every header and
keeps the node's address for `clientip.Peer`. Over the node's authenticated cluster link
the header is the machine's own word and needs no HMAC. From an unsigned request, or with
a wrong HMAC, the headers are dropped and the node's address is the client. Node trust
needs no setting, cannot be turned off and is independent of `trusted_proxies`: an empty
list still believes signed node clients, and listing a node's address is never needed. A
revoked or removed node fails the signature check (its keys are revoked and its membership
is read on each request), so it loses trust on its next request. The signed string is
unchanged, so mixed versions work: an older home ignores the extra headers, and an older
node, which sends none, shows as its own address. The cloud has its own
`[server] trusted_proxies` for `CF-Connecting-IP`, which takes CIDRs only.

## Design principles

- **Content-addressed identity.** A video is identified by a BLAKE3 hash of its content,
  so renames and moves never cause reprocessing.
- **Idempotent, resumable jobs.** Every pipeline stage can be re-run safely; outputs are
  written to a temp path and renamed atomically.
- **Modular stages.** Each pipeline stage has a small interface and talks only through
  the job store, so replacing the speech-to-text engine touches nothing else.
- **Graceful degradation.** If the pipeline is down, the library still browses and
  streams; if streaming is down, browse and search still work.

## Where to read next

- [Repository layout](repo-layout.md) to find the code for each service.
- [Local setup](local-setup.md) to run it.
- The epics under
  [specs/epics](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics) for the
  design of each feature.
