# `GET /status.json`: the home's status (Epic 53, story 53.03)

The supervisor (`maktaba-server serve`) answers this on the web port itself: it is never
proxied, needs no sign-in and works while the API, Postgres or the log index is down. Every
app reads it to show its status strip (story 53.06). Answered from memory, `Cache-Control:
no-store`.

```jsonc
{
  "v": 1,
  "at": "2026-11-03T10:15:00Z",
  "mode": "normal",                 // normal | safe | recovery
  "overall": "degraded",            // ok | degraded | down
  "components": {
    "database": "ok", "api": "ok", "streaming": "down",
    "processing": "ok", "search": "ok", "logs": "off"
  },
  "features_down": ["live_tv", "playback"],
  "banner": "streaming_down"        // left out when nothing is wrong
}
```

It names no host, path, version, count or error text: anyone who can reach the web port
(through the relay too) may read it. Details are for admins, on System health and the
recovery console (53.04).

## States

| State | Means |
|---|---|
| `ok` | runs and answers its checks |
| `starting` | not checked yet (the supervisor itself is starting, or the part is) |
| `degraded` | alive, but a check or a dependency fails (two checks 10 s apart agree), or one worker of several is down |
| `restarting` | exited, waiting to be started again |
| `down` | should run, but crash-looped and is cut off |
| `off` | turned off, held, not installed or waiting on a requirement in its Epic 51 desired state, or kept off for this boot: **off is not down**, and raises no banner |

A crash shows at once; a part that still runs but fails its checks shows after two checks
(about 20 s), and it clears the same way.

## Groups

Each key of `components` is a set of Epic 51 catalog ids
(`cmd/maktaba-server/internal/supervisor/statusmap.go`; a test fails when a process component
of the catalog is in no group):

| Group | Catalog ids | Down puts in `features_down` | `banner` |
|---|---|---|---|
| `database` | `core.postgres`, plus a `SELECT 1` on a connection of its own every 10 s | `all` | `database_down` |
| `api` | `core.api` | `all` | `api_down` |
| `streaming` | `core.streaming`, `livetv.slots` | `playback`, `live_tv` | `streaming_down` |
| `processing` | `core.pipeline`, `transcription` | `new_media`, `transcripts`, `thumbnails` | `processing_down` (admins only) |
| `search` | `search.semantic` | `semantic_search` | `search_down` |
| `logs` | `logs.index` (the log index, story 53.09) | `log_search` | none |

`overall` is `down` when `database` or `api` is restarting or down, else `degraded` when any
group is not `ok` or `off`, else `ok`. `banner` is the first group in the table's order that
is degraded, restarting or down and has a banner key.

Each change is a `status_group_changed` line in the supervisor's log (`group`, `from`, `to`).
A joined machine answers its own `/status.json`; the home's says nothing about it.
