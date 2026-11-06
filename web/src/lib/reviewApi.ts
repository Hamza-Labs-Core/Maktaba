// Epic 52 (stories 52.8-52.14): typed calls for word review. Every route is for home
// admins only on the server: no sign-in is 401, anyone else 403 (a library's write or admin
// role grants nothing). Refusals are problem+json with a top-level `code`: locked (with
// locked_by_name), stale, resolved, changed_since, not_active, text_required,
// unknown_action, invalid (with field).
//
//   GET  /api/review/flags                ?library&status&language&reason&video&show_locked&cursor&limit
//   GET  /api/review/flags/{id}           the flag with its context; takes a 5-minute soft lock
//   GET  /api/review/flags/{id}/clip      {url, start, end, expires_at}
//   POST /api/review/flags/{id}/resolve   {action, text?, apply_to_same_key?, force?, scope?}
//   POST /api/review/edits/{id}/undo, /api/review/batches/{id}/undo
//   GET  /api/review/summary
//   GET  /api/videos/{id}/transcript/edits, POST /api/videos/{id}/transcript/words
//   GET/PUT /api/libraries/{id}/word-review, GET /api/language-packs, /api/vocabulary...
import { api, apiUrl } from "./api";

// The shared codes: the Go API (api/internal/handlers/review/codes.go) and the pipeline
// (word_review/settings.py) name the same lists, and a test in each fails on a difference.
export const REASONS = [
  "low_confidence",
  "not_in_dictionary",
  "listeners_disagree",
  "quran_mismatch",
  "named_entity",
  "near_vocabulary",
  "manual",
  "previous_fix",
  "over_budget",
] as const;
export const STATUSES = [
  "open",
  "accepted",
  "corrected",
  "name",
  "dictionary",
  "skipped",
  "stale",
] as const;
export const SOURCES = [
  "whisper",
  "second_listen",
  "dictionary",
  "vocabulary",
  "quran",
  "gazetteer",
  "previous_fix",
] as const;
/** The reasons a library may turn off. */
export const SWITCHABLE = REASONS.slice(0, 6);

export type Reason = (typeof REASONS)[number];
export type Status = (typeof STATUSES)[number];
export type Source = (typeof SOURCES)[number];
export type Action = "accept" | "correct" | "name" | "dictionary" | "skip";

export interface Candidate {
  text: string;
  source: Source | string;
  score: number;
}

export interface QuranRef {
  surah: number;
  aya?: number;
  first?: number;
  last?: number;
  label_ar?: string;
  label_en?: string;
  expected?: string;
  missing?: string;
  kind?: "sub" | "ins" | "del";
}

export interface Flag {
  id: string;
  kind: "word" | "transcript";
  transcript_id: string;
  video_id: string;
  video_title: string;
  library_id: string;
  segment_seq: number;
  word_index: number;
  word_count: number;
  /** code points into context */
  char_start: number;
  char_len: number;
  start_sec: number | null;
  end_sec: number | null;
  original: string;
  context: string;
  language: string;
  direction: "rtl" | "ltr";
  reasons: string[];
  scores: Record<string, unknown> & { quran?: QuranRef; time_estimated?: boolean };
  priority: number;
  candidates: Candidate[];
  key: string;
  status: Status;
  resolution: string | null;
  locked_by: string | null;
  locked_by_name: string | null;
  locked_until: string | null;
  created_at: string;
}

export interface SamePreview {
  flag_id: string;
  video_id: string;
  video_title: string;
  context: string;
  start_sec: number | null;
}

export interface FlagDetail extends Flag {
  before: string;
  after: string;
  same_key_open: number;
  same_key_preview: SamePreview[];
  verse: QuranRef | null;
  reviewers_note: string;
}

export interface FlagPage {
  items: Flag[];
  next_cursor: string | null;
}

export interface Clip {
  url: string;
  start: number;
  end: number;
  expires_at: string;
}

export interface ResolveResult {
  status: Status;
  changed: number;
  edit_id: string | null;
  batch_id: string | null;
}

export interface LibrarySummary {
  library_id: string;
  name: string;
  open: number;
  flags_week: number;
  fixes_week: number;
  resolved: number;
  accepted_share: number;
}

export interface Summary {
  open: number;
  libraries: LibrarySummary[];
  languages: Record<string, number>;
  reasons: Record<string, number>;
  last_check: string | null;
}

export interface EditRow {
  id: string;
  segment_seq: number;
  char_start: number;
  char_len: number;
  before: string;
  after: string;
  flag_id: string | null;
  batch_id: string | null;
  user: string | null;
  created_at: string;
  undone_at: string | null;
  carried_from: string | null;
  active: boolean;
}

export interface WordReviewSettings {
  enabled?: boolean;
  backfill?: boolean;
  sensitivity?: "low" | "normal" | "high";
  budget_per_hour?: number;
  reasons?: Partial<Record<string, boolean>>;
  reviewers_note?: string;
}

export interface WordReviewView {
  settings: WordReviewSettings;
  backfill: { transcripts: number; checked: number; audio_hours: number; cpu_hours_left: number };
}

export interface PackRow {
  code: string;
  component: string | null;
  version: string;
  data_sha256: string | null;
  tokenizer: string | null;
  state: "loaded" | "off" | "missing" | "error";
  error: string | null;
  loaded_at: string | null;
}

export interface LanguagePacksView {
  packs: PackRow[];
  heard: Record<string, number>;
  components: Array<{
    id: string;
    phase?: string;
    reason?: string;
    install_size_mb?: number;
  }> | null;
}

export interface Vocab {
  id: string;
  scope: "home" | "library";
  library_id: string | null;
  language: string;
  term: string;
  key: string;
  kind: "word" | "name" | "term";
  variants: string[];
  source: "review" | "manual" | "import";
  uses: number;
  created_at: string;
}

function qs(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params))
    if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
  const s = q.toString();
  return s ? `?${s}` : "";
}

export const reviewApi = {
  flags: (
    f: {
      library?: string;
      status?: Status;
      language?: string;
      reason?: string;
      video?: string;
      show_locked?: boolean;
      cursor?: string | null;
      limit?: number;
    } = {}
  ) => api.get<FlagPage>(`/api/review/flags${qs(f)}`),
  flag: (id: string) => api.get<FlagDetail>(`/api/review/flags/${id}`),
  clip: (id: string) => api.get<Clip>(`/api/review/flags/${id}/clip`),
  resolve: (
    id: string,
    body: {
      action: Action;
      text?: string;
      apply_to_same_key?: boolean;
      force?: boolean;
      scope?: "library" | "home";
    }
  ) => api.post<ResolveResult>(`/api/review/flags/${id}/resolve`, body),
  undoEdit: (id: string) => api.post<{ undone: number }>(`/api/review/edits/${id}/undo`),
  undoBatch: (id: string) => api.post<{ undone: number }>(`/api/review/batches/${id}/undo`),
  summary: () => api.get<Summary>("/api/review/summary"),
  history: (videoId: string) =>
    api.get<{ items: EditRow[] }>(`/api/videos/${videoId}/transcript/edits`),
  fixWord: (
    videoId: string,
    body: {
      segment_seq: number;
      char_start: number;
      char_len: number;
      before: string;
      text: string;
      action?: "correct" | "name" | "dictionary";
      language?: string;
    }
  ) =>
    api.post<{ segment_seq: number; text: string; flag_id: string; edit_id: string | null }>(
      `/api/videos/${videoId}/transcript/words`,
      body
    ),
  settings: (libraryId: string) =>
    api.get<WordReviewView>(`/api/libraries/${libraryId}/word-review`),
  saveSettings: (libraryId: string, s: WordReviewSettings) =>
    api.put<{ settings: WordReviewSettings }>(`/api/libraries/${libraryId}/word-review`, s),
  languagePacks: () => api.get<LanguagePacksView>("/api/language-packs"),
  vocabulary: (
    f: { q?: string; kind?: string; language?: string; library?: string; scope?: string } = {}
  ) => api.get<{ items: Vocab[] }>(`/api/vocabulary${qs(f)}`),
  addVocab: (v: {
    scope: "home" | "library";
    library_id?: string;
    language: string;
    term: string;
    kind: Vocab["kind"];
    variants?: string[];
  }) => api.post<{ id: string; key: string }>("/api/vocabulary", v),
  editVocab: (id: string, v: { term: string; kind?: Vocab["kind"]; variants?: string[] }) =>
    api.put<{ id: string }>(`/api/vocabulary/${id}`, v),
  deleteVocab: (id: string) => api.delete<{ deleted: string }>(`/api/vocabulary/${id}`),
  vocabExportUrl: () => apiUrl("/api/vocabulary/export"),
  correctionsExportUrl: (library?: string) =>
    apiUrl(`/api/review/corrections/export${qs({ library })}`),
};

/** A code the server sent that this build does not know reads as unknown, never a crash. */
export function knownReason(code: string): code is Reason {
  return (REASONS as readonly string[]).includes(code);
}
