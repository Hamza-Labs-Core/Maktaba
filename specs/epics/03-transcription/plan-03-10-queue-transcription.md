# Plan 03-10 — Whole-library transcription on the queue engine

Status: built and running (feat/transcription); the library is being transcribed. Supersedes the legacy media lane
(`scan → probe → extract → transcribe → subtitle_gen → index`) for libraries that
come in through the queue engine (the mediaintel import), which never ran it.

## Goal

Every video in the library is transcribed — Arabic, each sentence with its exact
start and end — the transcript is saved next to the video on the share
(`.ar.srt` / `.ar.vtt`, which Plex picks up), feeds search (keyword now, semantic
with the multilingual e5 model) and the topic tags, and the whole run is visible
live in the control centre: progress, rate, ETA. Queue based: new videos are
transcribed by the same edges.

## Measured (M3 Ultra, 60-core GPU, 96 GB; whisper-large-v3-turbo, MLX, fp16)

| mode | throughput |
|---|---|
| `mlx_whisper.transcribe`, 1 stream, word timestamps | 35–42× realtime |
| 4 processes, word timestamps | 61× (plateau; 6 processes no better) |
| 4 processes, no word timestamps | 92× |
| batched decode, 1 process, batch 32 (fixed chunks, no fallback, no words) | 148× |
| batched, stock seek + per-row fallback + batched word alignment, 2 workers | 68× (live) |
| + timestamp rules on the GPU, encoder once a pass | ~100× (live) |
| + fallback windows reuse their encoding, alignment by row index | ~128× (live) |

Decoding the audio off the share (ffmpeg, NFS) is 2–3 s per 30 min. The GPU is the
only bottleneck; batching is the lever. The library is ~36,870 h of audio: about
12 days at 128×.

Where a pass's time went (24 real windows, before the fixes): encoder ~4 s, decoding
9–13 s, word alignment ~4.4 s of which the decoder is 0.2 s (it ran the encoder again).
Decoding was dominated by MLX-Whisper's `ApplyTimestampRules`, which copies the
tokens to the CPU and builds a NumPy mask row by row at every token (it also stops
MLX preparing the next step); `stt/rules.py` does the same rules as GPU array
operations, with OpenAI Whisper's semantics (MLX-Whisper's copy bounds the
"no earlier timestamp" mask by position, not value, so it never applied). Same text
on the benchmark windows. Silence skipping (VAD) was measured and left out: finished
transcripts are ~93 % speech and a silent window costs little in a batched pass.

## Design

### Stages (engine graph)

| stage | subject | pool | edges in | what |
|---|---|---|---|---|
| `transcribe` | video | `gpu` (per worker, size 32; `MAKTABA_STT_BATCH`) | `content_hash → transcribe (same)` | decode audio off the share, batched Whisper, store the transcript |
| `subtitle_gen` | video | `nas_write` (global, 4) | `transcribe → subtitle_gen (same)` | write `<name>.ar.srt` + `<name>.ar.vtt` next to the video; `subtitle_files` rows |
| `index` | video | `cpu` | `transcribe → index (same)` | e5 embeddings of the segments into a persistent Chroma |
| `tag` | season / library | `cpu` | `transcribe → tag (season_of, library_of)` | the tagger also reads the transcript |

`transcribe`, `subtitle_gen` and `index` stop being legacy stages: engine
implementations (`engine/stages/`), fingerprints, output hashes, skip-unchanged
(slot 0100). They form their own lane, `speech` (slot 0101), shown as
"Transcription" on the Processing page after Episodes.

### `transcribe`

- Input fingerprint: `{content_hash, model, language, TRANSCRIBE_VERSION}` (a re-run
  only when the file, the model or the version changes).
- Audio: `ffmpeg -i <local path> -vn -ac 1 -ar 16000 -f s16le -` into memory (no
  extract stage, no audio cache).
- A process-wide **batched transcriber** (`stt/batched.py`): one model, one GPU loop.
  Every running job submits its audio and awaits its result. The loop keeps a seek
  pointer per job and decodes one 30 s window of every active job per GPU pass
  (batch = active jobs, up to the pool size) — the stock `mlx_whisper.transcribe`
  algorithm (seek to the last complete timestamp, temperature fallback on low
  log-prob / high compression, no-speech skip, `condition_on_previous_text=False`)
  vectorised across files. Same text as stock; throughput of the batch.
- Each sentence (Whisper segment) carries its start and end; word timings (DTW
  alignment, all windows of a pass in one decoder call on the pass's encoder
  output, `stt/align.py`) snap the sentence bounds to its first and last word.
- Encoder output is computed once a pass and kept for a window decoded again at a
  warmer temperature. `mlx-whisper` is pinned: the decode builds on its internals,
  and the fast timestamp rules fall back to its own filter if they change.
- Memory: MLX's array limit and buffer cache are capped (`MAKTABA_STT_GPU_GB`,
  `MAKTABA_STT_GPU_CACHE_GB`); a worker admits files up to an audio-hours budget
  (`MAKTABA_STT_AUDIO_HOURS`) and holds new ones while the host's free memory is
  under `MAKTABA_STT_MIN_FREE_PCT`.
- Storage: `transcripts` (backend `mlx-batched`, model, language `ar`,
  `word_level`, `audio_track_id` NULL — migration makes it nullable) + one
  `transcript_segments` row per sentence (`start_sec`, `end_sec`, `text`,
  `confidence` = exp(avg log-prob), `metadata.no_speech`). Words are not stored row
  by row (≈ 900 M rows for the library); a segment's words go into
  `metadata.words` as `[[start, end, word], …]` only when asked (setting).
- Output hash: hash of the segment texts (an unchanged transcript does not
  re-run subtitles / index / tags).

### Files in place

`subtitle_gen` writes beside the video, atomically (temp + rename), same stem:
`Name.ar.srt`, `Name.ar.vtt`. An existing file not written by Maktaba (no
`maktaba` marker in its first cue/comment) is left alone and reported.

### Search

- Keyword: `transcript_segments.search_tsv` is generated — works as soon as segments exist.
- Semantic: `index` embeds segments with `intfloat/multilingual-e5-*` (`passage:`
  prefix; queries `query:`) into Chroma at `MAKTABA_CHROMA_DIR` (required); the API
  gets a `SemanticClient` (gRPC `Embed` + Chroma query) so `hybrid` really fuses.

### Tags

`tag` (season and loose videos) adds the transcript text as evidence (topic words,
sub-topics) beside titles and descriptions; transcript evidence is weighted per
rule settings, never hard-coded.

### Report (control centre)

A Transcription page: hours of audio done / total, videos done / total, rate
(× realtime, h/h), ETA, per library, recent failures; live over the existing
`/ws/queues` SSE. Settings: model, language, batch size, word timings,
subtitle files on/off.

Live speed: finished files lag the work (a worker decodes ~30 files side by side,
and a restart drops the files in flight), so each worker publishes counters to
`health_samples`, named by its slot (`MAKTABA_STT_SLOT`, set by
`tools/dev/stack.sh`): `stt.audio_sec:<slot>` (audio its seeks moved past) and
`stt.audio_lost:<slot>` (audio of files that failed, or were in flight at shutdown;
the health sampler takes a last round as the worker stops), plus GPU memory, files
in flight and windows. The report's "Decoding now" row and its ETA, and the
Processing page's Transcribe card ETA, use decoded less lost over the last 10
minutes.

### Rollout

1. Pilot one library, check text + timing + files.
2. Queue the whole library by priority
   (`python -m maktaba_pipeline.tools.queue_transcription --apply`): on Live TV in
   the next 48 h, then series episodes, then the rest. The workers run continuously
   (`tools/dev/stack.sh start 2`: one main worker, two transcription workers);
   failures are classified and retryable.
