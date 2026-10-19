// Fixtures for the Changes page tests and the Playwright screenshots: the shapes the
// API answers (api/internal/handlers/tvchange, page.go) for a staged lineup + schedule
// change, a draft, a live change and a failed one.
import type {
  Boundaries,
  ChangeList,
  Release,
  ScheduleChannelDiff,
  SchedulePage,
  SlotsView,
  StagingConfig,
  TvChangeDetail,
} from "../../../lib/tvChanges";

export const ADMIN_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
export const OTHER_ADMIN_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
export const CH_QURAN = "00000000-0000-4000-8000-0000000000a1";
export const CH_HADITH = "00000000-0000-4000-8000-0000000000b2";
export const CH_SEERAH = "00000000-0000-4000-8000-0000000000c3";
export const CH_KIDS = "00000000-0000-4000-8000-0000000000d4";
export const RELEASE_ID = "5e1ea5e0-0000-4000-8000-000000000042";

export const stagingConfig: StagingConfig = {
  enabled: true,
  require_second_admin: false,
  swap_wait: "boundary",
  boundary_max_wait_minutes: 30,
  quiet_window: "03:00-05:00",
  quiet_max_viewers: 0,
  auto_promote: {
    art: "approval",
    binary: "approval",
    lineup: "approval",
    live_slots: "approval",
    rollback: "approval",
    schedule: "approval",
    settings: "approval",
  },
  keep_warm_minutes: 15,
  rollback_retention_hours: 24,
  param_change_push: "approve",
};

const base = {
  source: { kind: "editor" },
  approved_by: [] as string[],
  created_at: "2026-06-15T19:40:00Z",
  updated_at: "2026-06-15T20:05:00Z",
  verify_report: {},
  diff: {},
  events: [],
  users: { [ADMIN_ID]: "mahmoud", [OTHER_ADMIN_ID]: "aisha" } as Record<string, string>,
  audit: [],
};

/** #42: staged from the channel editor, verified, waiting for an approval. */
export const readyChange: TvChangeDetail = {
  ...base,
  id: "c0000000-0000-4000-8000-000000000042",
  number: 42,
  release_id: RELEASE_ID,
  state: "ready",
  kinds: ["lineup", "schedule", "settings", "art"],
  title: "Ramadan grid and the Seerah channel",
  source: { kind: "editor" },
  created_by: ADMIN_ID,
  staged_at: "2026-06-15T19:58:00Z",
  ready_at: "2026-06-15T20:01:00Z",
  allowed_actions: ["approve", "cancel", "schedule", "verify"],
  approved_by: [],
  users: { ...base.users },
  audit: [
    {
      id: 7,
      at: "2026-06-15T19:58:00Z",
      action: "stage",
      actor_user_id: ADMIN_ID,
      actor_source: "admin",
      payload: { from: "draft", to: "draft" },
    },
    {
      id: 9,
      at: "2026-06-15T20:01:00Z",
      action: "pass",
      actor_source: "system",
      payload: { from: "verifying", to: "ready" },
    },
  ],
  events: [
    {
      id: 1,
      at: "2026-06-15T19:40:00Z",
      actor_user_id: ADMIN_ID,
      actor_source: "admin",
      to_state: "draft",
      action: "create",
      detail: {},
    },
    {
      id: 2,
      at: "2026-06-15T19:58:00Z",
      actor_source: "system",
      from_state: "draft",
      to_state: "staged",
      action: "stage",
      detail: {},
    },
    {
      id: 3,
      at: "2026-06-15T20:00:00Z",
      actor_source: "system",
      from_state: "staged",
      to_state: "verifying",
      action: "start_verify",
      detail: {},
    },
    {
      id: 4,
      at: "2026-06-15T20:01:00Z",
      actor_source: "system",
      from_state: "verifying",
      to_state: "ready",
      action: "pass",
      detail: {},
    },
  ],
  release: {
    id: RELEASE_ID,
    digest: "d42",
    kinds: ["lineup", "schedule", "settings", "art"],
    sched_gen: 7,
    streaming_version: "0.33.0",
    created_at: "2026-06-15T19:58:00Z",
    live: false,
    changes: [],
  },
  diff: {
    lineup: {
      added: [{ id: CH_SEERAH, number: 130, name: "Seerah" }],
      removed: [{ id: CH_KIDS, number: 150, name: "Kids classics" }],
      renumbered: [{ id: CH_HADITH, from: 105, to: 115 }],
      renamed: [{ id: CH_QURAN, from: "Quran", to: "Quran recitations" }],
      changed: [{ id: CH_QURAN, number: 101, name: "Quran recitations", fields: ["mode_config"] }],
    },
    schedules: [
      {
        id: CH_QURAN,
        number: 101,
        name: "Quran recitations",
        reason: "regenerated",
        divergence_at: "2026-06-15T21:00:00Z",
      },
      {
        id: CH_HADITH,
        number: 115,
        name: "Hadith",
        reason: "regenerated",
        divergence_at: "2026-06-15T20:30:00Z",
      },
      { id: CH_SEERAH, number: 130, name: "Seerah", reason: "new" },
    ],
    settings: {
      tv_output: [
        { key: "fps", from: 30, to: 25 },
        { key: "video_bitrate_kbps", from: 4500, to: 6000 },
      ],
    },
    art: [{ id: CH_HADITH, number: 115, name: "Hadith", what: ["logo_path"] }],
    binary: null,
  },
  verify_report: {
    at: "2026-06-15T20:01:00Z",
    interim: true,
    passed: true,
    warnings: 1,
    checks: [
      {
        check: "slot.health",
        result: "skip",
        value: "the two-slot runtime (story 33.1) is not installed",
      },
      { check: "guide.coverage", channel_id: CH_QURAN, result: "pass", value: "24 h covered" },
      { check: "lineup.sanity", channel_id: CH_SEERAH, result: "warn", value: "no logo" },
      { check: "guide.coverage", channel_id: CH_HADITH, result: "pass", value: "24 h covered" },
      {
        check: "channel.block",
        channel_id: CH_QURAN,
        result: "pass",
        value: "the programme on the air is kept",
      },
    ],
  },
};

/** #43: a draft from TV settings, queued behind #42. */
export const draftChange: TvChangeDetail = {
  ...base,
  id: "c0000000-0000-4000-8000-000000000043",
  number: 43,
  state: "draft",
  kinds: [],
  title: "TV settings",
  source: { kind: "settings" },
  created_by: OTHER_ADMIN_ID,
  created_at: "2026-06-15T20:04:00Z",
  allowed_actions: ["approve", "cancel", "schedule", "stage"],
  diff: { pending: { settings: { tv_output: { fps: 25 } } } },
  pending_summary: {
    channels: [{ id: CH_QURAN, number: 101, name: "Quran recitations", op: "art" }],
    settings: ["tv_output"],
  },
};

/** #41: on the air. */
export const liveChange: TvChangeDetail = {
  ...base,
  id: "c0000000-0000-4000-8000-000000000041",
  number: 41,
  release_id: "5e1ea5e0-0000-4000-8000-000000000041",
  state: "live",
  kinds: ["lineup"],
  title: "Channel plan of 14 June",
  source: { kind: "write_channels", stage: "write_channels" },
  created_at: "2026-06-14T09:00:00Z",
  swapped_at: "2026-06-14T09:30:00Z",
  approved_by: [ADMIN_ID],
  allowed_actions: ["rollback"],
  verify_report: {
    checks: [
      { check: "guide.coverage", channel_id: CH_QURAN, result: "pass", value: "24 h covered" },
    ],
  },
};

/** #40: failed verification. */
export const failedChange: TvChangeDetail = {
  ...base,
  id: "c0000000-0000-4000-8000-000000000040",
  number: 40,
  state: "failed",
  kinds: ["schedule"],
  title: "Regenerate Hadith",
  source: { kind: "schedule" },
  created_at: "2026-06-13T22:00:00Z",
  end_reason: "verification_failed",
  allowed_actions: [],
  verify_report: {
    checks: [
      { check: "guide.coverage", channel_id: CH_HADITH, result: "pass", value: "24 h covered" },
      {
        check: "channel.block",
        channel_id: CH_HADITH,
        result: "fail",
        value: "the new schedule would cut the programme on the air",
      },
    ],
  },
};

export const changeList: ChangeList = {
  items: [draftChange, readyChange, liveChange, failedChange],
  in_flight: readyChange.id,
  config: stagingConfig,
  users: { [ADMIN_ID]: "mahmoud", [OTHER_ADMIN_ID]: "aisha" },
};

export const slots: SlotsView = {
  runtime: "single",
  slots: [
    {
      host_id: "home",
      slot: "blue",
      role: "live",
      state: "healthy",
      release_id: liveChange.release_id,
    },
  ],
};

export const releases: { items: Release[] } = {
  items: [
    {
      id: liveChange.release_id!,
      digest: "d41",
      kinds: ["lineup"],
      sched_gen: 6,
      streaming_version: "0.33.0",
      created_at: "2026-06-14T09:00:00Z",
      live: true,
      changes: [liveChange.id],
    },
  ],
};

export const schedulePage: SchedulePage = {
  channels: readyChange.diff.schedules!,
  page: 1,
  pages: 1,
  total: 3,
};

export const quranSchedule: ScheduleChannelDiff = {
  channel: readyChange.diff.schedules![0]!,
  from: "2026-06-15T21:00:00Z",
  to: "2026-06-16T21:00:00Z",
  staged: true,
  unchanged: false,
  before: [
    {
      start_at: "2026-06-15T21:00:00Z",
      end_at: "2026-06-15T22:00:00Z",
      kind: "program",
      title: "Tafsir 2",
    },
    {
      start_at: "2026-06-15T22:00:00Z",
      end_at: "2026-06-15T23:00:00Z",
      kind: "program",
      title: "Tafsir 3",
    },
  ],
  after: [
    {
      start_at: "2026-06-15T21:00:00Z",
      end_at: "2026-06-15T21:30:00Z",
      kind: "program",
      title: "Taraweeh",
    },
    {
      start_at: "2026-06-15T22:00:00Z",
      end_at: "2026-06-15T23:00:00Z",
      kind: "program",
      title: "Tafsir 3",
    },
  ],
};

export const boundaries: Boundaries = {
  at: "2026-06-15T20:10:00Z",
  swap_wait: "boundary",
  boundary_max_wait_minutes: 30,
  last_switch_at: "2026-06-15T20:40:00Z",
  channels: [
    {
      channel_id: CH_QURAN,
      number: 101,
      name: "Quran recitations",
      switch_at: "2026-06-15T20:40:00Z",
      on_air: "Tafsir 1",
      capped: true,
    },
    {
      channel_id: CH_HADITH,
      number: 115,
      name: "Hadith",
      switch_at: "2026-06-15T20:30:00Z",
      on_air: "Hadith 1",
    },
    { channel_id: CH_SEERAH, number: 130, name: "Seerah", switch_at: "2026-06-15T20:10:00Z" },
  ],
};

export const notifications = {
  unread: 2,
  items: [
    {
      id: "n0000000-0000-4000-8000-000000000003",
      kind: "tv.change.ready",
      severity: "warn" as const,
      title_key: "notify.tv.change.ready",
      params: { number: 42, title: readyChange.title, kinds: readyChange.kinds },
      link: `/admin/live-tv/changes/${readyChange.id}`,
      audience: "admins",
      created_at: "2026-06-15T20:01:00Z",
      read: false,
    },
    {
      id: "n0000000-0000-4000-8000-000000000002",
      kind: "tv.change.failed",
      severity: "error" as const,
      title_key: "notify.tv.change.failed",
      params: { number: 40, title: failedChange.title, failed: 1 },
      link: `/admin/live-tv/changes/${failedChange.id}`,
      audience: "admins",
      created_at: "2026-06-13T22:05:00Z",
      read: false,
    },
    {
      id: "n0000000-0000-4000-8000-000000000001",
      kind: "tv.change.live",
      severity: "success" as const,
      title_key: "notify.tv.change.live",
      params: { number: 41, title: liveChange.title },
      link: `/admin/live-tv/changes/${liveChange.id}`,
      audience: "admins",
      created_at: "2026-06-14T09:30:00Z",
      read: true,
    },
  ],
};

/** The API answer for a path (GET), for the mocked api.get of the tests. */
export function answer(path: string): unknown {
  const p = path.split("?")[0]!;
  if (p === "/api/tv/changes") return changeList;
  if (p === "/api/tv/slots") return slots;
  if (p === "/api/tv/releases") return releases;
  if (p === "/api/notifications") return notifications;
  const m = p.match(/^\/api\/tv\/changes\/([^/]+)(\/.*)?$/);
  if (m) {
    const c = changeList.items.find((x) => x.id === m[1]);
    if (!m[2]) return c;
    if (m[2] === "/boundaries") return boundaries;
    if (m[2] === "/schedule-diff") return path.includes("channel=") ? quranSchedule : schedulePage;
  }
  if (p === "/api/rules") return { sections: [] };
  throw new Error(`no fixture for ${path}`);
}
