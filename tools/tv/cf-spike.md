# Cloudflare Stream spike (Epic 32 story 32.6, AC7)

The design of remote viewing (Epic 32 §7) assumes some Cloudflare Stream behaviour that
Cloudflare does not document. `maktaba-api cloudflare spike` checks it against a real
account, once, before story 32.7 is merged. Its findings update `design.md` §7 and the
runbook (32.12).

It costs a few cents: one minute of live input, the minutes a test player fetches, and
the recording (deleted at the end).

## What it does

1. Reads the storage use, creates a **temporary signing key** and a live input
   `maktaba-spike-<time>` (recording `automatic`, `requireSignedURLs`, `timeoutSeconds`).
2. Pushes a test pattern with ffmpeg over RTMPS (or SRT), encoded like a channel:
   H.264 high, 2 s closed GOP, no B-frames, 6 Mb/s, AAC 128k.
3. Polls the input's lifecycle until it is live and learns the in-progress **video id**.
4. Checks, and writes into a JSON report:

| Check | Report fields |
|---|---|
| (a) a token whose `sub` is the video id plays; one whose `sub` is the input id; no token | `token_sub_video_manifest`, `token_sub_input_manifest`, `unsigned_manifest` |
| (b) `exp` per segment: a token of `-exp-ttl`, the playlist and a segment fetched after it expired | `playlist_after_exp`, `segment_after_exp`, `exp_enforced_per_segment` |
| (c) time to live, time to playable, newest segment's program date time lag | `time_to_live_seconds`, `time_to_playable_seconds`, `program_date_time_lag_seconds` |
| (d) ingest drops (`-drops 10s,60s`): same video id after reconnecting? | `drops[]` |
| (e) delete the recording right after the broadcast: is storage freed, how fast? | `storage_minutes_*`, `storage_freed`, `storage_freed_after_seconds` |
| (f) concurrent live input limit (`-limit-probe N`) | `input_limit_created`, `input_limit_error` |
| (g) a live output (`-output-url`): does Cloudflare report its state; does it survive a reconnect? | `output_answer`, `output_reports_status`, `output_survived_reconnect` |

5. Deletes the input, the recording and the temporary key (unless `-keep`).

Glass-to-glass latency needs eyes: run with `-clock` (burns the wall clock into the
picture), open the HLS URL printed in the log in a player, and compare with a clock.
Repeat with `-low-latency` for LL-HLS.

## Running it

You need: the account id, an API token with **Stream: Edit** and **Account Analytics:
Read**, the customer subdomain code (Stream, Live inputs, any HLS manifest URL
`customer-<code>.cloudflarestream.com`; it is learned from the broadcast if omitted),
and ffmpeg built with TLS (`ffmpeg -protocols` lists `rtmps`).

The token is read from the environment only (never a flag, so it stays out of the
process list and shell history), and is never printed. Stream keys are redacted in the
report.

```sh
export MAKTABA_CF_ACCOUNT_ID=<account id>
export MAKTABA_CF_API_TOKEN=<token>          # or MAKTABA_CF_API_TOKEN_FILE=/path/to/file
export MAKTABA_CF_CUSTOMER_CODE=<code>       # optional

# the whole spike: 2-minute broadcast, a 10 s and a 60 s drop, limit probe up to 60
maktaba-api cloudflare spike -duration 2m -drops 10s,60s -timeout-seconds 30 \
  -limit-probe 60 -out cf-spike.json

# LL-HLS and the burned-in clock for glass-to-glass
maktaba-api cloudflare spike -low-latency -clock -duration 3m

# a live output to a test RTMP server (its key in MAKTABA_CF_OUTPUT_KEY), check (g)
MAKTABA_CF_OUTPUT_KEY=<key> maktaba-api cloudflare spike -output-url rtmp://example/live -drops 10s

# a real channel instead of the test pattern (an MPEG-TS file, looped)
maktaba-api cloudflare spike -source channel.ts -ingest srt
```

Flags: `-customer-code`, `-ingest rtmps|srt`, `-duration`, `-drops`, `-timeout-seconds`,
`-low-latency`, `-exp-ttl`, `-limit-probe`, `-output-url`, `-keep`, `-ffmpeg`, `-source`,
`-clock`, `-out`. The JSON report goes to stdout (and `-out`); progress goes to stderr.
A failed check is listed in `errors` and the run still cleans up.

## What to do with the findings

- (a) fails for the video id but works for the input id → design §7.6 fallback: `sub` is
  the input id and the pool keeps a cool-down of one `token_ttl` per input.
- (b) segments ignore `exp` → keep `token_ttl` short (the heartbeat refreshes it).
- (d) a 10 s drop gives a new video id → `timeoutSeconds` must be raised (the relay
  restarts within 1 s, design §7.4).
- (e) storage is not freed promptly → the storage guard (§7.7) needs more headroom.
- (f) the limit → `tv_remote.max_channels` default.
- (g) → story 32.13 (README Q13).
