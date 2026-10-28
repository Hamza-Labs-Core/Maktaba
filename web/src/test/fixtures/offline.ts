// Epic 47 fixtures for the Queues page's offline pieces (tests and screenshots).
import type {
  DuplicateGroup,
  FileAction,
  LibraryTrash,
  OfflineItem,
  OfflineSummary,
  YouTubeSource,
} from "../../lib/queueApi";

export const LIB = "0a1b2c3d-0000-4000-8000-00000000000a";

export function summary(over: Partial<OfflineSummary> = {}): OfflineSummary {
  return {
    library_id: LIB,
    name: "Islamic lectures",
    youtube: {
      sources: 3,
      sources_on: 2,
      wanted: 4,
      backfill: 120,
      downloading: 1,
      downloaded: 812,
      failed: 2,
      skipped: 5,
      gone: 1,
      last_refresh: "2026-10-28T10:00:00Z",
      last_fetched: "2026-10-28T10:30:00Z",
      problems: [],
    },
    not_mp4: 37,
    converting: 3,
    duplicates: 2,
    trash: { entries: 4, bytes: 3 * 2 ** 30, next_purge_at: "2026-11-20T00:00:00Z" },
    ...over,
  };
}

export function source(over: Partial<YouTubeSource> = {}): YouTubeSource {
  return {
    id: "5a000000-0000-4000-8000-000000000001",
    library_id: LIB,
    kind: "channel",
    url: "https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa",
    external_id: "UCaaaaaaaaaaaaaaaaaaaaaa",
    folder: "Lessons",
    mode: "new_and_backfill",
    added_at: "2026-10-01T00:00:00Z",
    options: { max_height: 1080 },
    status: "ok",
    error: null,
    last_checked_at: "2026-10-28T10:00:00Z",
    last_fetched_at: "2026-10-28T10:30:00Z",
    downloads: {
      wanted: 2,
      backfill: 40,
      downloading: 0,
      done: 300,
      failed: 1,
      skipped: 2,
      gone: 1,
    },
    ...over,
  };
}

export function fileAction(over: Partial<FileAction> = {}): FileAction {
  return {
    id: "fa000000-0000-4000-8000-000000000001",
    library_id: LIB,
    library_name: "Islamic lectures",
    video_id: "v0000000-0000-4000-8000-000000000001",
    video_title: "Lecture 04",
    job_id: 41,
    kind: "convert",
    state: "done",
    old_path: "smb://nas/O/Lectures/04.mkv",
    new_path: "smb://nas/O/Lectures/04.mp4",
    trash_path: "smb://nas/O/.maktaba/trash/convert/2026-10-28/Lectures/04.mkv",
    old_size: 734003200,
    new_size: 701234176,
    verify: null,
    error: null,
    purge_after: "2026-11-27T00:00:00Z",
    purged_at: null,
    created_at: "2026-10-28T09:00:00Z",
    updated_at: "2026-10-28T09:05:00Z",
    restorable: true,
    restore_queued: false,
    ...over,
  };
}

export const trash: LibraryTrash[] = [
  {
    library_id: LIB,
    name: "Islamic lectures",
    entries: 4,
    bytes: 3 * 2 ** 30,
    next_purge_at: "2026-11-20T00:00:00Z",
    last_purge_at: "2026-10-27T03:00:00Z",
  },
];

export function offlineItem(over: Partial<OfflineItem> = {}): OfflineItem {
  return {
    id: 501,
    stage: "convert_mp4",
    state: "running",
    subject_kind: "video",
    subject_id: "v0000000-0000-4000-8000-000000000002",
    subject_label: "Lecture 05",
    library_id: LIB,
    priority: 150,
    attempts: 1,
    max_attempts: 2,
    outcome: null,
    error_cause: null,
    created_at: "2026-10-28T09:00:00Z",
    started_at: "2026-10-28T09:01:00Z",
    finished_at: null,
    progress: { step: "encode", steps: [], done_units: 40, total_units: 100 },
    step: "convert",
    waiting_for_conversion: false,
    downloading: null,
    error: null,
    ...over,
  };
}

export function group(over: Partial<DuplicateGroup> = {}): DuplicateGroup {
  return {
    id: "d0000000-0000-4000-8000-000000000001",
    library_id: LIB,
    library_name: "Islamic lectures",
    tier: "same_video",
    status: "open",
    keeper_video_id: null,
    reason: null,
    created_at: "2026-10-28T09:00:00Z",
    decided_by: null,
    decided_at: null,
    job: null,
    members: [
      {
        video_id: "v0000000-0000-4000-8000-000000000011",
        path: "smb://nas/O/Seerah/01.mp4",
        is_copy: false,
        score: null,
        title: "Seerah 01",
        library_id: LIB,
        library_name: "Islamic lectures",
        size_bytes: 512 * 2 ** 20,
        duration_sec: 3600,
        width: 1920,
        height: 1080,
        video_codec: "h264",
        container: "mov,mp4",
        bitrate_kbps: 1200,
        viewers: 2,
        completed: 1,
        last_watched: "2026-10-20T10:00:00Z",
      },
      {
        video_id: "v0000000-0000-4000-8000-000000000012",
        path: "smb://nas/O/Playlists/Seerah 01.mp4",
        is_copy: false,
        score: null,
        title: "Seerah 01 (playlist)",
        library_id: LIB,
        library_name: "Islamic lectures",
        size_bytes: 300 * 2 ** 20,
        duration_sec: 3601,
        width: 1280,
        height: 720,
        video_codec: "h264",
        container: "mov,mp4",
        bitrate_kbps: 700,
        viewers: 0,
        completed: 0,
        last_watched: null,
      },
    ],
    ...over,
  };
}
