# Story 3.9 — Diarization (opt-in, off by default)

## Description

Tag each segment with a speaker label when the library opts in.

## Acceptance criteria

- Library setting `diarize = true` enables `pyannote.audio`'s pretrained
  diarization pipeline. Default is `false`.
- When enabled, the diarizer runs **before** STT on the same audio
  stream (or in parallel if memory permits) and produces a list of
  `(start, end, speaker_id)` intervals. The transcribe stage assigns
  each segment's `speaker` to whichever interval covers its midpoint.
- Speaker IDs are local to the video (`Speaker 1`, `Speaker 2`, …) at
  this stage; matching to known speakers in the `speakers` table is a
  follow-up story (deferred to v1.1).
- Diarization is gated by a process-global `diarization_lock` semaphore
  (default 1) because pyannote is GPU-greedy.

## Test cases

- `test_diarize_off_by_default` — fixture without `diarize` setting →
  segments have `speaker = None`.
- `test_diarize_assigns_speakers` — fixture with two speakers →
  segments alternate between `Speaker 1` and `Speaker 2` matching the
  fixture's known boundaries.
- `test_diarize_disabled_skips_pipeline` — `diarize = false` → pyannote
  is never imported (verify import is lazy).

## Edge cases

- **Diarization disagrees mid-segment.** When a single STT segment
  spans two speakers, the segment is **split** at the diarization
  boundary into two `transcript_segments` rows with the same `seq`
  prefix and a `.a/.b` suffix in `metadata.split_from`. Word-level
  text re-assignment happens only when word timestamps are present.
- **Diarization fails entirely.** Segments are committed without
  speaker labels; the failure is recorded on the transcript row but
  does not fail the job.
