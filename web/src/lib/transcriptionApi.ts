// Typed client for GET /api/transcription/summary (api/internal/handlers/
// transcription). The live report of the whole-library transcription run.
import { api } from "./api";

/** The programme's stages, in pipeline order. */
export const TRANSCRIPTION_STAGES = ["transcribe", "subtitle_gen", "index"] as const;
export type TranscriptionStage = (typeof TRANSCRIPTION_STAGES)[number];

export interface TranscriptionTotals {
  videos: number;
  videos_transcribed: number;
  audio_hours: number;
  hours_transcribed: number;
  hours_remaining: number;
  segments: number;
}

/** State of each video's latest job in one stage. */
export interface TranscriptionStageCounts {
  stage: string;
  pending: number;
  running: number;
  done: number;
  failed: number;
}

export interface TranscriptionWindow {
  window_sec: number;
  jobs: number;
  audio_hours: number;
  audio_hours_per_hour: number;
  realtime_factor: number;
  avg_job_rtf: number | null;
}

/** What the workers are decoding now (finished videos lag it). */
export interface TranscriptionLive {
  window_sec: number;
  workers: number;
  audio_hours: number;
  realtime_factor: number;
}

export interface TranscriptionLibrary {
  library_id: string;
  name: string;
  videos: number;
  videos_done: number;
  hours: number;
  hours_done: number;
}

export interface TranscriptionFailure {
  job_id: number;
  video_id: string;
  title: string;
  library_name: string | null;
  stage: string;
  error_cause: string | null;
  error: string | null;
  finished_at: string | null;
  attempts: number;
}

export interface TranscriptionSummary {
  generated_at: string;
  totals: TranscriptionTotals;
  stages: TranscriptionStageCounts[];
  rate: {
    last_10m: TranscriptionWindow;
    last_1h: TranscriptionWindow;
    /** absent from an older API */
    live?: TranscriptionLive;
  };
  eta: { seconds: number | null; at: string | null; basis: "live" | "10m" | "1h" | null };
  libraries: TranscriptionLibrary[];
  failures: TranscriptionFailure[];
}

export const transcriptionApi = {
  summary: () => api.get<TranscriptionSummary>("/api/transcription/summary"),
};
