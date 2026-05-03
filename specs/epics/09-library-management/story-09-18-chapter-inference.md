# Story 9.18 — Chapter inference from transcript topic shifts

Architecture §4.6 calls for "inferred chapters from transcript-level
topic shifts (cosine drop between adjacent segment embeddings >
threshold)." This was missing from every prior epic per REVIEW.md
§2.7.a; this story adds the generation step. Chapter delivery (HLS
DATERANGE + JSON) is owned by Epic 8 Story 8.12.

**AC-1 — Pipeline stage.**
- **Given** a video that has reached `INDEXED` and a library setting
  `chapter_inference: true` (default true; per Story 9.1),
- **When** the chapter-inference stage runs,
- **Then** it processes the video's segment embeddings sequentially,
  computes cosine similarity between adjacent windows
  (`window_segments`, default 5), and emits a chapter boundary wherever
  the cosine drop exceeds `chapter_drop_threshold` (default 0.35),
  subject to a minimum chapter duration `min_chapter_sec` (default 60).
  The job is recorded in `processing_jobs` with `stage='chapter_infer'`
  (the FSM in arch §3 needs the matching state — see REVIEW.md §1.3.b).

**AC-2 — Output table.**
- **Given** the inference produces N boundaries,
- **When** committed,
- **Then** N+1 rows are inserted into `chapters` with `source='inferred'`,
  `seq` 0…N, and `title` left NULL (filled by AC-3).
- Existing `source='inferred'` rows for the video are deleted in the
  same transaction so re-inference is idempotent.

**AC-3 — Title generation.**
- **Given** an inferred boundary,
- **When** the labeler runs,
- **Then** the top-3 segments inside the chapter window are concatenated
  and asked of the embedder for the nearest token; the resulting bigram
  becomes the title (capped at 80 chars). If the embedder is
  unavailable, the title falls back to `"Chapter N"`.

**AC-4 — Suppression and override.**
- **Given** a video with embedded or manual chapters,
- **When** inference runs,
- **Then** existing `embedded` and `manual` rows are preserved unchanged;
  inferred rows that fall within a manual or embedded chapter's range
  are suppressed (no overlap) per Epic 8 Story 8.12 AC-1's priority
  merge.
- **Given** a user runs `POST /api/videos/{id}/chapters/reinfer`,
- **When** processed,
- **Then** the inference re-runs with current settings; the existing
  inferred rows are replaced atomically.

**AC-5 — Disabled per library.**
- **Given** library setting `chapter_inference: false`,
- **When** the orchestrator schedules stages,
- **Then** the stage is skipped for new videos; existing inferred
  chapters are preserved (the user must purge them manually if
  desired).

**Test cases:**
- Unit: a fixture transcript with three obvious topic shifts produces
  exactly three boundaries.
- Unit: a perfectly uniform transcript produces zero boundaries (i.e.
  one chapter spanning the whole video).
- Integration: re-running inference with `min_chapter_sec=120` collapses
  short chapters from the 60 s baseline run.
- Integration: a video with embedded chapters has no inferred chapters
  in the same time ranges.
- Integration: stage failure (e.g., embedder unreachable) sets
  `stage='chapter_infer'` job to `failed` with `error.code='embedder-down'`;
  retry succeeds.

**Edge cases:**
- A transcript with fewer than `2 * window_segments` segments — produce
  exactly one chapter spanning the video; no boundaries.
- Cosine drop threshold tuned too low (every adjacent pair is a
  boundary) — `min_chapter_sec` enforces a minimum, so the result is
  bounded by `duration_sec / min_chapter_sec` chapters.
- A video whose `chapter_inference` setting was flipped after inference
  ran — old inferred rows remain until the user re-infers or purges.
