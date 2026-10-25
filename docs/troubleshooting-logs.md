# When something fails: where to look in the logs

Maktaba.app writes every service's output to one file:

```
~/Library/Logs/Maktaba/server.log
```

The admin **Logs** page reads the same lines. Three line shapes share the file:

| Source | Shape | Example |
|---|---|---|
| Go services (api, streaming, TV slots) | slog text: `ts=… level=… msg=… service=… event=…` | `level=ERROR msg=http_error service=api … event=http_error` |
| Python pipeline workers | structlog JSON: `{"event": "...", "level": "...", ...}` | `{"event": "stage_failed", "job_id": …}` |
| The supervisor (`maktaba-server serve`) | `[tag] level=… text` | `[supervisor] level=error api exited: …` |

Every Go line carries `service` (`api`, `streaming`, `tv-slot`) and an
`event` key: search for `event=<name>`. Failures are `level=WARN` or
`level=ERROR`. Successful requests are only logged by the API
(`event=http_request`); the streaming service logs only the ones that failed.

Secrets never appear in these lines: stream URLs are logged without their
query (the `?sig=` signature), DSNs are not logged, and the pipeline's
redaction masks token/secret/password/dsn keys and `X-Plex-Token=`, `sig=`
and `user:pass@` inside messages.

## A video does not play

Follow the session from the API to the encoder to the player:

1. **`event=stream_session_opened`** (api): `session_id`, `video_id`,
   `user_id`, `mode` (`direct`, `transcode`), `start_sec`. Note the
   `session_id`; every later line names only it.
   If the open itself failed: **`event=stream_open_failed`** (api) with the
   streaming service's error, or an **`event=http_error`** line.
2. **`event=vod_session_opened`** / **`vod_session_reused`** (streaming):
   the decision: `mode`, `verdict` (why that mode), `source`
   (container/video/audio codec), `height`, `pid` of ffmpeg.
   Refusals: `vod_open_not_probed`, `vod_open_probe_failed`,
   `vod_slots_exhausted`, `vod_transcode_start_failed` (ffmpeg could not be
   spawned: `input`, `err`).
3. **`event=vod_transcode_exited`** (streaming): how ffmpeg ended.
   `outcome=finished|stopped|failed`; a failure carries `exit_code`, `err`
   and the last lines of ffmpeg's `stderr` (the decoder or input error).
   `event=vod_transcode_fallback` is a hardware encode that failed early and is
   running again in software.
4. **`event=stream_request_failed`** (streaming): every `/stream/*` request
   answered 400 or more: `path` (no query), `status`, `problem` (the problem
   name, e.g. `transcode-stopped` for a 410, `segment-not-found`,
   `signed-url-expired`, `video-bytes-missing`), `duration_ms`. A playlist or
   segment a cold transcode has not written yet is logged at INFO, not WARN.
5. **`event=playback_failed`** (api, reported by the player): what the
   browser saw after the session opened. `engine` (`hls.js`, `native`,
   `direct`), `outcome` (`fatal`: the error screen; `reopen`: it opened the
   video again; `stalled`: no progress for 15 s), `code` (an hls.js error
   detail such as `fragLoadError` / `bufferAppendError`, or
   `media_err_<n>`: 2 network, 3 decode, 4 format not supported;
   `reopen_limit`: the stream was gone three times in two minutes),
   `error`, `http_status`, `url` (no query), `position_sec`, `user_agent`.
6. **`event=vod_session_closed`** / **`vod_session_reaped`** (streaming):
   `reason=api` (the player closed it), `transcode-failed` (a reopen found
   its transcode ended short; `stderr` attached), `idle` (nothing fetched
   from it; `idle_ms`).

Direct play (the file itself): **`event=vod_direct_open_failed`** (the file
could not be opened: `path`, `err`; ERROR for a permission denial or an
unmounted share, WARN when it is missing) and **`event=vod_direct_read_failed`**
(a read failed mid-stream: `offset`, `written`, `err`, e.g. a NAS I/O
error). A segment or playlist that exists but cannot be read is
**`event=vod_file_read_failed`**.

A quick filter for one session:

```bash
grep '<session_id>' ~/Library/Logs/Maktaba/server.log
```

Every failure of the last hour, any service:

```bash
grep -E 'level=(WARN|ERROR)|"level": ?"(warning|error)"|level=error|level=warn' ~/Library/Logs/Maktaba/server.log | tail -100
```

## An API call fails

- **`event=http_request`**: one line per request (`method`, `path`,
  `status`, `duration_ms`, `request_id`); WARN for a 5xx.
- **`event=http_error`**: every 5xx with its reason: `type`, `detail` and
  `err` (the underlying error, never sent to the client), plus the same
  `request_id`. The error page and problem+json body show that request id.
- **`event=http_panic`**: a handler panicked: `method`, `path`,
  `request_id`, `stack`.

## Live TV

- `event=channel_ts_ended`: a channel's MPEG-TS stream to a viewer ended
  with an error (not the viewer leaving).
- `event=hdhr_stream_failed` / `hdhr_tuners_busy`: HDHomeRun (Plex) tunes.
- `event=stream_request_failed` covers the TV routes too.

## The pipeline (scans, transcription, enrichment)

Every line logged while a job runs carries its `job_id`, `stage` and
`video_id`.

- `stage_failed` (`job_id`, `stage`, `subject_id`, `kind`, `retryable`,
  `error`) and `stage_handler_failed` (an unexpected exception, with
  traceback), then `stage_failure_outcome`: what became of the job
  (`outcome=retry|parked|terminal`, `not_before`, `failure_class`,
  `parked_on`).
- `dispatch_unknown_stage`: a job for a stage no handler serves.
- `share_listing_failed` (`directory`, `errno`): a share's folder could not be
  listed, so files there look missing until it can.
  `share_root_unreachable` / `share_root_reachable`: a share root went away
  or came back.
- `scanner.stat_failed`, `scanner.is_dir_failed`, `scanner.is_file_failed`
  (WARN with `errno`): a file the scan skipped because it could not be read.
- `grpc_call_failed` (`method`, `error_type`, `error`): a call from the API
  (search, transcription, models…) that failed; `grpc_call_raised` for an
  unexpected exception.
- `unhandled_exception` (`source=main|thread|asyncio`): an exception nothing
  caught, with traceback. Library warnings (grpc, asyncio, httpx) are
  forwarded into the same JSON lines with `logger=<name>`.

## The supervisor (Maktaba.app itself)

Lines start with `[supervisor]`, `[serve]`, `[update]`, `[postgres]`. A child
that exits says how (`exit code`, signal, uptime) and when it restarts
(`restart #n, k/max exits in <window>`); a crash loop ends with
`marked failed`. Update runs also keep their own log under the app's data
folder, `updates/logs/<run>.log`.

## The playback failure report (for client authors)

A player posts each failure after a session opened to

```
POST /api/stream/sessions/{session_id}/errors
{ "video_id", "mode", "engine", "outcome", "code", "message",
  "http_status", "url", "position_sec" }
```

It answers 202 and stores nothing. It is limited to one line per session and
outcome per second, and 30 per user per minute. `web/src/lib/playbackReport.ts` is the web (and phone) client; the
API side is `api/internal/handlers/streaming/playback_report.go`.
