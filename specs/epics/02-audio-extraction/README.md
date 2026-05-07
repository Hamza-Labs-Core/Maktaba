# Epic 02 — Audio Extraction

**Goal.** From a probed video, pick the right audio track, extract it as
mono 16 kHz signed-16-bit PCM (Whisper's required input shape), and feed it
into the transcriber **without** writing an intermediate WAV file when the
backend can consume a stream. Record what we extracted in `audio_tracks`
so the transcript can be tied back to a specific track even if the file
later changes.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/media/`
(`ffmpeg.py`, `audio.py`) and the `extract` stage in
`pipeline/src/maktaba_pipeline/pipeline/stages/extract.py`.

**Out of scope.** STT itself (Epic 3); subtitle extraction (Epic 4);
audio-format conversion of original files (we never modify source media).

## Stories

| # | Title | File |
|---|-------|------|
| 2.1 | Probe the audio tracks | [story-02-01-audio-probe.md](story-02-01-audio-probe.md) |
| 2.2 | Track selection | [story-02-02-track-selection.md](story-02-02-track-selection.md) |
| 2.3 | Stream extraction (no intermediate WAV by default) | [story-02-03-stream-extraction.md](story-02-03-stream-extraction.md) |
| 2.4 | Audio resource accounting | [story-02-04-resource-accounting.md](story-02-04-resource-accounting.md) |

## Dependency notes

- Depends on Epic 1 Stories 1.1, 1.2 (a video row in `DISCOVERED` is the
  precondition for `probe`) and Epic 6 Stories 6.1–6.3 (job queue,
  claim, heartbeat).
- Story 2.1 transitions video state to `PROBED` or `READY_NO_AUDIO`
  (see [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)).
- Story 2.3's stream output is consumed by Epic 3 (Transcription).
