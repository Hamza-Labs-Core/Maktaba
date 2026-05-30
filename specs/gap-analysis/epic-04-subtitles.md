# Epic 04 — Subtitles: Spec-vs-Implementation Gap Analysis

**Verdict:** Subtitle *primitives* exist and are well-tested in isolation, but the entire epic is **unwired** — no `subtitle_gen` stage handler, no embedded-extraction RPC binding, no scanner sidecar discovery, no `is_embedded` migration, no proto changes, and the `transcript_segments_v` view lacks its required index. The library is a shelf of parts no runtime path reaches.

## Scope of code reviewed

- `pipeline/src/maktaba_pipeline/subtitle/{__init__,formats,generator,manager,extractor}.py`
- `pipeline/src/maktaba_pipeline/runtime.py`, `grpc_server.py`
- `pipeline/src/maktaba_pipeline/scanner/`, `discovery/`, `audio/probe.py`
- `shared/db/migrations/0015_subtitle_files.{sql,sqlite.sql}`, `0051_transcript_segments_view.{sql,sqlite.sql}`
- `streaming/internal/handlers/subtitle.go`, `streaming/internal/server/server.go`
- `api/internal/grpcclients/pipeline/{pipeline,realclient}.go`
- No `shared/proto/*.proto` exists anywhere in the repo (only stdlib protos under `pipeline/.venv`).

---

## Story 4.1 — Generate SRT and VTT from `transcript_segments`

| AC | Text (abbrev) | Status | Evidence | Gap |
|----|---------------|--------|----------|-----|
| 4.1-a | `subtitle_gen` stage produces `<root>/.maktaba/subs/<hash>.<lang>.{srt,vtt}` + source-dir alias copy | **missing** | `runtime.py:193` `Stage.SUBTITLE_GEN: _placeholder_handler("subtitle_gen")`; no override registered anywhere | No stage handler. Placeholder logs + `mark_done` without producing any file. Path layout is `~/.maktaba/cache/subtitles/{video_id}/{source}.{lang}.{fmt}` (`manager.py:99-100`), **not** the spec's `<root>/.maktaba/subs/<hash>.<lang>.<fmt>`. No alias-copy code anywhere. |
| 4.1-b | Rows inserted into `subtitle_files` with `is_external=false`,`is_embedded=false`; orchestrator advances to `INDEXED` | **partial/unwired** | `manager.py:183 register_subtitle` correctly sets flags; but `grep` for `register_subtitle` callers in `pipeline/src` returns **zero hits** | Registry helper exists and is correct, but is never called by any stage. No orchestrator coupling between subtitle_gen completion and `INDEXED`. |
| 4.1-c | Cue text HTML-escaped (`<`→`&lt;`,`>`→`&gt;`,`&`→`&amp;`) after wrapping, before framing | **partial** | `formats.py:91-100 escape_vtt_text` correct for VTT; `formats.py:79-88 escape_srt_text` only rewrites `-->`, does **not** escape `<`,`>`,`&` | AC says "written to **either** format" with the three escapes. SRT path leaves `<script>` verbatim. Test `test_cue_text_html_escaped` (SRT+VTT) would fail for SRT. Also escape is applied at render, not "after wrapping" — no wrapping is ever invoked (see 4.2). |
| 4.1-d | Atomic temp write to `.maktaba/.tmp/<uuid>.{srt,vtt}` then `os.replace`; partial temp removed on retry | **partial** | `manager.py:103-126 write_atomic` does sibling-`.tmp-{pid}-{n}` + `os.replace` + fsync | Atomicity primitive exists but temp path is `.<name>.tmp-{pid}-{n}` next to dest, not `.maktaba/.tmp/<uuid>`. No retry/cleanup orchestration since no stage calls it. `test_atomic_replace_on_retry` cannot pass — no worker path. |
| 4.1-e | Read-only source dir → sidecar still written, row inserted, WARN `kind=alias_copy_failed`, job NOT failed | **missing** | No alias-copy logic exists in `subtitle/` or any stage | Entirely absent. |
| 4.1 edge | `.maktaba/` created `0755`; failure → `error.kind="sidecar_dir"` | **missing** | `write_atomic` uses `mkdir(parents=True, exist_ok=True)` with default mode; no `sidecar_dir` error kind | No mode `0755`, no typed error. |
| 4.1 edge | Source basename collision → alias skipped, `kind=alias_collision` | **missing** | none | Absent. |
| 4.1 edge | Existing-entity one-pass escape | **complete (VTT only)** | `formats.py:98-100` replaces `&` first — `&amp;` → `&amp;amp;` as specified | Correct for VTT; SRT does not escape at all. |

**SRT round-trip / VTT round-trip / clamp tests** (`tests/subtitle/test_generator.py`) pass for the *primitive* but do not exercise the stage.

## Story 4.2 — SRT/VTT formatting & line wrapping

| AC | Text (abbrev) | Status | Evidence | Gap |
|----|---------------|--------|----------|-----|
| 4.2-a | `max_line_chars=42`, `max_lines=2`, configurable per library | **partial/wrong-default** | `formats.py:106-107` `DEFAULT_CUE_LINE_CHARS=80`, `DEFAULT_CUE_MAX_LINES=2` | Default is **80**, spec mandates **42**. Not library-configurable (no plumb from `library_mgmt/config.py`). `wrap_cue` exists but is never called by the generator (`generator.py` does not import `wrap_cue`). |
| 4.2-b | Line breaks favor sentence-end / clause-end punctuation (incl. `؟،؛`), then word boundary | **missing** | `formats.py:110-168 wrap_cue` is purely greedy word-packing; no `.?!؟`/`,;،؛:` awareness | Punctuation-priority breaking entirely absent. `test_wrap_breaks_at_sentence` would fail. |
| 4.2-c | Cues never overlap; merge if gap < `merge_gap_sec=0.05`; split if > `max_cue_sec=6.0` proportionally on word timestamps | **missing** | `grep merge_gap/max_cue/0.05/6.0` → no matches; `generator.py:88-91` only nudges `end=start+0.001` per-cue | No merge, no split, no overlap resolution, no `split_method` metadata. `test_no_overlap_after_merge_or_split`, `test_long_segment_split_proportionally` would fail. |
| 4.2-d | Arabic punctuation glyphs preserved (no normalization) | **complete (vacuously)** | No normalization code exists; text passes through `escape_*` unchanged | Arabic glyphs survive only because no wrapping/normalization runs at all — passes by absence of harm, not by design. |
| 4.2-e | VTT `<v Speaker>` only when diarized & `speaker IS NOT NULL`; speaker label HTML-escaped | **complete** | `generator.py:122-123` emits `<v {escape_vtt_text(cue.speaker)}>` only `if cue.speaker`; `segments_to_cues` carries `speaker` through | Correct. `test_speaker_tag_only_when_diarized`, `test_speaker_label_escaped` should pass. |
| 4.2 edge | No word timestamps → linear split, `metadata.split_method="linear"` | **missing** | no split logic | Absent. |
| 4.2 edge | Over-long token on own line, one DEBUG violation/file | **partial** | `formats.py:151-166` places over-long token alone, allows overflow; no DEBUG log | Overflow handling present, logging absent; and `wrap_cue` itself is dead code (uncalled). |
| 4.2 edge | Grapheme-cluster wrapping for bidi/surrogates | **missing** | `formats.py:134` `text.split()` then `len()` on `str` = code-point count, not grapheme | Not grapheme-safe. |

## Story 4.3 — External subtitle auto-discovery

| AC | Text (abbrev) | Status | Evidence | Gap |
|----|---------------|--------|----------|-----|
| 4.3-a | Scanner matches `^<basename>(\.(lang))?\.(srt\|vtt\|ass\|ssa)$` against siblings | **unwired** | `extractor.py:308-336 discover_sidecars` implements name matching; `grep discover_sidecars` in `scanner/` + `discovery/` → **zero hits** | Discovery function exists but the scanner never calls it. Regex differs from spec: implementation also accepts `.sub`, `forced/sdh/cc/hi` flag tokens; uses substring/`split('.')` parsing, not the exact spec regex (functionally close, but `ass`/`ssa` only — matches). |
| 4.3-b | Each match → `subtitle_files` row `is_external=true`,`is_embedded=false`,`language`,`format`,`transcript_id=NULL`,`path=absolute` | **missing** | No code constructs `SubtitleRecord(source=EXTERNAL)` from a sidecar; no caller of `register_subtitle` | No row is ever written for sidecars. |
| 4.3-c | `.ass`/`.ssa` recorded but not converted; deferred to Streaming first-request | **missing** | streaming `subtitle.go` has `SrtToVtt` (line ~135) but only an SRT grammar; no ASS/SSA path; nothing records the row to defer | Neither the record nor the deferred conversion exists. |
| 4.3-d | Rescan idempotent; uniqueness `(video_id,language,format,is_external,path)` | **partial** | migration `0015_subtitle_files.sql:42` unique index is `(video_id,language,format,source)` — **not** including `path` | Uniqueness key differs from spec (`source` vs `is_external,path`); multiple external subs for same lang would collide on the partial unique index, contradicting 4.3 "Multiple external subtitles … All are kept". `test_rescan_idempotent` untestable — no scan path. |
| 4.3 edge | External wins over generated; auto row `is_active=false` | **missing** | `subtitle_files` has no `is_active` / `is_default` / `is_preferred` column (migration 0015 columns: id,video_id,transcript_id,language,format,source,path,byte_size,sha256,is_embedded,is_external,metadata,created_at,deleted_at) | No precedence model; columns absent. |
| 4.3 edge | First external `is_default=true`, manifest `DEFAULT=YES` | **missing** | no `is_default` column | Absent. |
| 4.3 edge | Subtitle moved → path updated; gone → soft-delete | **partial** | `manager.py:225 soft_delete_subtitle` exists and is correct | Helper exists but no scanner reconciliation invokes it. |

## Story 4.4 — Embedded subtitle extraction (+ `is_embedded` schema)

| AC | Text (abbrev) | Status | Evidence | Gap |
|----|---------------|--------|----------|-----|
| 4.4-a | Probe records `media_info.has_subtitles=true` + `(index,codec,language)` list in `raw_ffprobe` | **partial** | `audio/probe.py:127` `has_subtitles=any(codec_type==subtitle)`; `:137` `raw_ffprobe=payload` (full ffprobe JSON) | `has_subtitles` correct. `raw_ffprobe` stores the *raw* payload, not the spec's curated `(index,codec,language)` list — consumers must re-parse. Acceptable-ish but not as specified. |
| 4.4-b | Migration `000X_subtitle_files_is_embedded.sql` adds `is_embedded` + index `subtitle_files_video_kind (video_id,is_external,is_embedded)` | **missing** | `grep is_embedded shared/db/migrations` → only `0015_subtitle_files.{sql,sqlite}`. Column was folded into 0015 directly; **no** `subtitle_files_video_kind` index exists in any migration | Story 4.4 is declared "single owner" of a dedicated migration; instead the column was pre-baked into 0015 with no dedicated migration and the required `subtitle_files_video_kind` index is **absent**. `test_migration_adds_is_embedded` (index check) fails. |
| 4.4-c | `pipeline.proto` adds `ExtractEmbeddedSubtitle` RPC + request/response messages; `architecture.md §9.9` updated | **missing** | No `shared/proto/` or any project `.proto` exists (`find -name '*.proto'` → only `.venv` stdlib). RPC dispatched by string `"ExtractEmbeddedSubtitle"` over JSON in `grpc_server.py:143` with `_identity_(de)serializer` | No proto contract at all; the RPC is a hand-rolled JSON-over-gRPC generic handler. Response has no `codec`/`language`/`cached` fields — handler returns `{"body": <str>}` only (`grpc_server.py:115`). |
| 4.4-d | RPC validates `stream_index` against `media_info.raw_ffprobe`; out-of-range/non-subtitle → `INVALID_ARGUMENT` `unknown_subtitle_stream` | **missing** | `grpc_server.py:105-115` validates only `path` is str and `stream_index` is int; then calls `self._subtitle_extractor` which is **never set** (`serve_grpc:182-183` instantiates `PipelineService()` with no extractor) | No DB cross-check against `raw_ffprobe`, no `INVALID_ARGUMENT`/`unknown_subtitle_stream`, and the path is dead: `_subtitle_extractor is None` → always `RuntimeError("subtitle extractor not configured")`. REVIEW §5.2 input-validation gap unresolved. |
| 4.4-e | Valid call extracts as VTT to `.maktaba/subs/<hash>.<lang>.embedded.vtt`; idempotent, 2nd returns `cached=true` | **missing** | `extractor.py:180 extract_embedded` can run ffmpeg, but no caller wires path/idempotency/cached flag; response shape lacks `cached` | No caching, no `cached` flag, wrong path scheme, unreachable. |
| 4.4-f | Text codecs converted via ffmpeg; bitmap (PGS/dvdsub) → `UNIMPLEMENTED` `unsupported_subtitle_codec` | **partial** | `extractor.py:45-52` flags image-based; `:193-194` raises `ExtractSubtitleError("image_based")` | Image-based detection exists but maps to a generic `RuntimeError`, not gRPC `UNIMPLEMENTED`/`unsupported_subtitle_codec`. List uses `dvd_subtitle` (spec says `dvdsub`) — codec-name mismatch risk. Unreachable regardless. |
| 4.4-g | Extracted row `is_external=false`,`is_embedded=true` | **partial** | `manager.py:189` sets `is_embedded` for `SubtitleSource.EMBEDDED`; no caller | Correct helper, never invoked. |
| 4.4-h | Extracted VTT sanitized identically to 4.1 | **missing** | `extract_embedded` writes ffmpeg output directly; no `escape_vtt_text` post-pass | ffmpeg output is not re-sanitized; hostile S_TEXT/UTF8 passes through. |
| 4.4 edge | Per-pair file-lock for concurrent extracts | **missing** | none | Absent. |

## Story 4.5 — Live VTT serving (read-side, contract only)

| AC | Text (abbrev) | Status | Evidence | Gap |
|----|---------------|--------|----------|-----|
| 4.5-a | View `transcript_segments_v` with `(video_id,transcript_id,seq,start_sec,end_sec,text,speaker,is_active)` + index `(video_id,start_sec)` | **partial** | `0051_transcript_segments_view.sql:16-34` creates the view (extra cols ok); **no index** in either `.sql` or `.sqlite.sql`; `transcript_segments` only has a speaker index (`0014`), none on `(video_id,start_sec)` | The required `(video_id, start_sec)` index does not exist anywhere. `test_view_index_supports_window_query` (EXPLAIN uses index) fails. View column naming uses `segment_id`/`transcript_language` aliases — superset of spec, acceptable. |
| 4.5-b | Only `is_active=true` transcripts visible | **complete** | `0051…sql:33` `WHERE t.is_active = true` (sqlite: `= 1`) | Correct. `test_view_excludes_superseded_transcripts` should pass. |
| 4.5-c | Write paths never lock view rows beyond a single-segment txn (row-level locks / SQLite WAL) | **partial/external** | Owned by Epic 3 §3.6 (`0013_segment_commit_function.sql`); not re-verified here | Contract dependency; out of this epic's code. The *consumer* side is also unwired: streaming `subtitle.go:42 ServeAuto` calls `h.Transcripts.Stream(...)` but `deps.Transcripts` is an injected interface never bound to a concrete view-querying impl (`server.go:90-91`); API `videos/segments.go:135` and `search/search.go` query base `transcript_segments`, not `transcript_segments_v`. The view has **zero query callers** repo-wide. |

---

## Top gaps by impact

1. **`subtitle_gen` stage is a placeholder — the entire epic's producer side never runs.** `runtime.py:193` binds `Stage.SUBTITLE_GEN` to `_placeholder_handler` which only logs and `mark_done`. No code anywhere imports `generate_srt`/`generate_vtt`/`register_subtitle` outside the `subtitle/` package and its unit tests. Stories 4.1 and 4.2 produce **no files and no DB rows** in any real pipeline run. (Worst gap.)

2. **`ExtractEmbeddedSubtitle` is permanently dead.** `serve_grpc` (`grpc_server.py:182-183`) constructs `PipelineService()` with no `subtitle_extractor`; the handler always raises `RuntimeError("subtitle extractor not configured")`. No proto contract exists; response lacks `codec`/`language`/`cached`; no stream-index validation (REVIEW §5.2 unresolved). Story 4.4 is non-functional end-to-end.

3. **Scanner never discovers sidecars.** `discover_sidecars` exists in `extractor.py` but is called by nothing in `scanner/` or `discovery/`. Story 4.3 produces zero `subtitle_files` rows; the `is_active`/`is_default` precedence columns the edge cases require don't exist in migration 0015.

4. **Story 4.5 view lacks its mandated `(video_id, start_sec)` index**, and no code (streaming, API, or pipeline) ever queries `transcript_segments_v`. The streaming `Transcripts` streamer is an unbound interface; live-VTT reads (`subtitle.go ServeAuto`) cannot return data.

5. **SRT cue text is not HTML-escaped** (`escape_srt_text` only rewrites `-->`), violating 4.1-c "either format"; and `wrap_cue` default is 80 chars vs spec's 42 and is dead code (the generator never wraps), violating 4.2-a/b/c (no merge, split, or punctuation-aware breaking exists at all).
