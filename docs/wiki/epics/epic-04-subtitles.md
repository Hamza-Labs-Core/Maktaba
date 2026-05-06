# Epic 04 — Subtitles

**Phase.** Pipeline (M3 — Outputs).
**Owner.** Pipeline Service ·
`pipeline/src/maktaba_pipeline/media/subtitles/` and the `subtitle_gen`
stage. Embedded extraction lives in the same module but runs lazily on
first request, not as a pipeline stage.

> **Goal.** Convert finalised transcripts into well-formed `.srt` and
> `.vtt` sidecar files; auto-discover external subtitle files shipped
> with the video; extract embedded subtitle tracks from MKVs on demand.
> The on-disk artifacts are for portability — Streaming Service can
> render live VTT directly from `transcript_segments` (architecture
> §4.5).

Source: [README](../../../specs/epics/04-subtitles/README.md) ·
Architecture §3.5 (Subtitle Generator), §4.5 (subtitle handling).

`subtitle_gen` is the canonical stage name fixed in
[Epic 1 Story 1.6](../../../specs/epics/01-scanner/story-01-06-video-state-machine.md)
(resolves REVIEW §1.3.b).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 4.1 | Generate SRT and VTT from `transcript_segments` | Core | [HLB-24](../linear-map.md) | [story-04-01](../../../specs/epics/04-subtitles/story-04-01-generate-from-segments.md) | [plan-04-01](../../../specs/epics/04-subtitles/plan-04-01-generate-from-segments.md) |
| 4.2 | SRT/VTT formatting & line wrapping | Core | [HLB-25](../linear-map.md) | [story-04-02](../../../specs/epics/04-subtitles/story-04-02-formatting-wrapping.md) | [plan-04-02](../../../specs/epics/04-subtitles/plan-04-02-formatting-wrapping.md) |
| 4.3 | External subtitle auto-discovery | Core | [HLB-26](../linear-map.md) | [story-04-03](../../../specs/epics/04-subtitles/story-04-03-external-discovery.md) | [plan-04-03](../../../specs/epics/04-subtitles/plan-04-03-external-discovery.md) |
| 4.4 | Embedded subtitle extraction (`is_embedded` schema) | Core | [HLB-27](../linear-map.md) | [story-04-04](../../../specs/epics/04-subtitles/story-04-04-embedded-extraction.md) | [plan-04-04](../../../specs/epics/04-subtitles/plan-04-04-embedded-extraction.md) |
| 4.5 | Live VTT serving (read-side, contract only) | Gate | [HLB-28](../linear-map.md) | [story-04-05](../../../specs/epics/04-subtitles/story-04-05-live-vtt-contract.md) | [plan-04-05](../../../specs/epics/04-subtitles/plan-04-05-live-vtt-contract.md) |

> Linear IDs from [linear-map.md](../linear-map.md).

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 4.1, 4.2 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) |
| 4.3 | [admin/library-config.html](../../../web/mockups/admin/library-config.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) |
| 4.4 | — | [streaming-flow.drawio](../../../specs/diagrams/streaming-flow.drawio) |
| 4.5 | [mockup-11-03-video-player](../../../web/mockups/mockup-11-03-video-player.html) | [streaming-flow.drawio](../../../specs/diagrams/streaming-flow.drawio) · [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) |

---

## DB tables owned

| Table | Purpose |
|-------|---------|
| `subtitle_files` | One row per discovered or generated subtitle: `format` (`srt` / `vtt`), `language`, `path`, `is_external`, `is_embedded`. Story 4.4 owns the `is_embedded` column migration (resolves REVIEW §1.1.c). |
| `transcript_segments_v` (view) | SQL view filtered by `transcripts.is_active = true`. Read source for both SRT/VTT generation (4.1) and live VTT (4.5). |

---

## API endpoints owned

This epic owns no REST endpoints directly. Subtitle enumeration
(`GET /api/videos/{id}/subtitles`) is owned by the API service
(Epic 7 Story 7.7). Live VTT serving lives in the Streaming service
(Epic 8 Story 8.11) and conforms to the read contract this epic
documents in Story 4.5.

---

## gRPC services owned

| Service · RPC | Purpose |
|---|---|
| `Pipeline.ExtractEmbeddedSubtitle(ExtractEmbeddedSubtitleRequest) → ExtractEmbeddedSubtitleResponse` | Lazy on-demand extraction of an embedded text-based subtitle stream from an MKV. Request: `{video_id, stream_index}`. Response: `{path, codec, language, cached}`. |

---

## LISTEN/NOTIFY channels

None owned. The live-VTT path consumes `segments.committed` (owned by
Epic 3 Story 3.6) transparently through the `transcript_segments_v`
view.

---

## Dependencies

**Depends on.**
- Epic 03 — `transcript_segments` and the `is_active` view (Story 3.5,
  3.6).
- Epic 01 — Story 4.3 runs as part of Story 1.1's scan walk; the
  `subtitle_gen` stage is in the FSM owned by Story 1.6.
- Epic 02 — Story 4.4 reuses the FFmpeg subprocess primitives from
  Story 2.3.

**Depended on by.**
- Epic 08 (Streaming) — Story 8.11 renders live VTT at manifest time
  from `transcript_segments_v` and calls
  `Pipeline.ExtractEmbeddedSubtitle` for embedded streams.
- Epic 07 (API) — Story 7.7 enumerates `subtitle_files` for clients.

---

## Key technical decisions

- **Generated from segments, never re-read from disk.** Both SRT and VTT
  are produced atomically from `transcript_segments_v` on
  `subtitle_gen`. `os.replace()` provides idempotency on retry.
- **Grapheme-cluster line wrapping.** Length is measured with
  `regex.findall(r"\X", s)` (extended grapheme clusters), not code
  points or display columns — the only correct way to count Arabic
  diacritic-laden text. Hard limit ≤ 42 clusters/line, ≤ 2 lines/cue.
- **External subtitles auto-discovered during scan.** Regex
  `^<basename>(?:\.(?P<lang>[a-z]{2,3}))?\.(?:srt|vtt|ass|ssa)$`. ASS/SSA
  conversion deferred to first request (lazy on serve).
- **Embedded extraction is on-demand.** It's a gRPC method, not a
  pipeline stage. Text-based streams (`S_TEXT/UTF8`) supported in v1;
  image-based (PGS, VOBSUB) deferred to v1.1.
- **Live VTT comes from the DB, not a file.** Streaming serves
  `application/x-mpegurl` referencing a VTT URL that the Streaming
  service renders from `transcript_segments_v` per request — the
  on-disk file is not load-bearing for playback.
- **HTML escape order matters.** `&` → `&amp;` first, *then* `<` → `&lt;`
  and `>` → `&gt;`, applied *after* line wrapping, so wrap-width
  arithmetic doesn't trip over multi-byte entities.

---

## Libraries / dependencies introduced

- **FFmpeg** (via `FFmpegRunner` from Epic 02 Story 2.3) — embedded
  extraction.
- **`regex`** module (PyPI) — `\X` grapheme-cluster matching, which
  stdlib `re` does not support.
- Custom Python writers in
  `pipeline/src/maktaba_pipeline/media/subtitles/` — `srt_writer.py`,
  `vtt_writer.py`.

---

## Test coverage summary

- **Round-trip:** every fixture transcript → SRT → re-parse without
  drift; same for VTT.
- **Wrapping:** `test_wrap_respects_max_line_chars` over Arabic +
  English + emoji; `test_long_segment_split_proportionally` splits at
  word boundaries with timing redistribution.
- **HTML escape:** `<`, `>`, `&` round-trip safely; entity ordering is
  tested explicitly.
- **Auto-discovery:** `test_external_srt_discovered`,
  `test_rescan_idempotent` over a fixture tree with mixed `.srt`,
  `.vtt`, `.ass`.
- **Atomicity:** `test_atomic_replace_on_retry` proves the writer is
  crash-safe; partial files are never left visible.
- **Live VTT contract:** the view returns segments only when
  `transcripts.is_active = true`; readers see all-or-nothing per the
  per-segment commit transaction.
