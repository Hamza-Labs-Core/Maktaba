# Epic 04 — Subtitles

**Goal.** Convert finalized transcripts into well-formed `.srt` and `.vtt`
sidecars; auto-discover external subtitle files shipped with the video;
extract embedded subtitle tracks from MKVs on demand. The Streaming
Service can render live VTT from `transcript_segments` directly
(architecture §4.5), so the **on-disk** subtitle artifacts are for
portability (a Plex or VLC user opening the same folder) and for clients
that prefer file URLs over manifest-embedded subtitles.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/media/subtitles.py`
and the `subtitle_gen` stage. Embedded extraction lives in the same module
but runs lazily on first request, not as a pipeline stage.

`subtitle_gen` is the canonical stage name in
[Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)
(resolves REVIEW §1.3.b).

**Out of scope.** Burning subtitles into video (the player renders them);
translation between languages (deferred per architecture Appendix B).

## Stories

| # | Title | File |
|---|-------|------|
| 4.1 | Generate SRT and VTT from `transcript_segments` | [story-04-01-generate-from-segments.md](story-04-01-generate-from-segments.md) |
| 4.2 | SRT/VTT formatting & line wrapping | [story-04-02-formatting-wrapping.md](story-04-02-formatting-wrapping.md) |
| 4.3 | External subtitle auto-discovery | [story-04-03-external-discovery.md](story-04-03-external-discovery.md) |
| 4.4 | Embedded subtitle extraction (with `is_embedded` schema) | [story-04-04-embedded-extraction.md](story-04-04-embedded-extraction.md) |
| 4.5 | Live VTT serving (read-side, contract only) | [story-04-05-live-vtt-contract.md](story-04-05-live-vtt-contract.md) |

## Dependency notes

- Stories 4.1, 4.2 require completed transcripts from
  [Epic 3](../03-transcription/README.md).
- Story 4.4 owns the `subtitle_files.is_embedded` schema migration
  (resolves REVIEW §1.1.c).
- Story 4.5 documents the read view that the Streaming Service consumes;
  the producer side lives in
  [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md).
- Story 4.3 runs as part of scanning
  ([Epic 1 Story 1.1](../01-scanner/story-01-01-file-discovery.md)).
