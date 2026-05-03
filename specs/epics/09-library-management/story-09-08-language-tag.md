# Story 9.8 — Auto-categorization: language tag

After `TRANSCRIBED`, the language detected by Whisper is written to
`videos.detected_language` (§5.2).

**AC-1 — Single-language assignment.**
- **Given** a transcript with `language='ar'` from STT,
- **When** the transcribe stage completes,
- **Then** `videos.detected_language = 'ar'` in the same transaction
  that flips state.

**AC-2 — Multi-audio overrides.**
- **Given** a video with multiple audio tracks transcribed (library
  `multi_audio=true`),
- **When** stats are computed,
- **Then** the *primary* track's language goes on `videos.detected_language`;
  per-track languages live on the transcripts rows.

**AC-3 — Confidence threshold.**
- **Given** Whisper's language detection confidence < 0.6,
- **When** assigning,
- **Then** `detected_language = 'und'` (undetermined); the user can
  manually set it via `PATCH /api/videos/{id}` (extends Epic 7 Story
  7.4).

**Test cases:**
- Unit: low-confidence fixture → `und`.
- Integration: PATCH user-set language overrides STT's value and is
  preserved across re-processing.

**Edge cases:**
- A library with `language: "ar"` (forced) — the user-pinned value is
  always written, regardless of STT confidence (the user knows their
  archive better than Whisper does).
- A code-switched audio (Arabic-English mix) — Whisper picks one;
  cross-language search via Chroma still works because embeddings are
  multilingual.
