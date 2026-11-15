---
schema: 1
kind: pr
pr: 1407
title: "Streaming: a segment cut short is an error, never a whole segment"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1407 merged as 0f0842455. When a home process exited mid-segment, the Apple apps could play
  a truncated segment as if it were whole: Go sent any body over 2 KB chunked, and URLSession
  takes a chunked body that ends with an orderly close (a FIN) as complete (1 MB of 8 MB came
  back 200 with no error). With a `Content-Length` every client treats a short body as an error.
- The home now sends a length on session segments, every playlist (channel ones included),
  sidecar and auto subtitles (`httpx.SetLength`, next to each handler's own Content-Type), and
  on a TV slot's disk fallback (still sendfile). The front door's `copyResponse` ends a copy
  that fails partway with `panic(http.ErrAbortHandler)`, so the client never gets a last chunk.
  The Apple StreamProxy cuts the player's connection when the bytes it got differ from the
  home's length (skipped for encoded bodies, HEAD, 204, 304). Android (OkHttp) and Chrome and
  Firefox already rejected a cut body. Only auto subtitles are newly built whole in memory.
- Review fixes: CodeQL flagged a helper that wrote bodies away from each handler's
  Content-Type as reflected XSS, so the helper only sets the length now; affected-tests needed a
  test beside `channel/handler.go` (`framing_test.go`).

## What we learned
- A transport that can't tell a cut body from a whole one (URLSession with chunked framing)
  needs the length from the server; fix it where the bytes are written.
- CodeQL reads a body writer split from its Content-Type as an XSS sink: keep the write next
  to the type.

## Open
- Found while chasing the StreamProxy test flake (#1405), which is still open.
