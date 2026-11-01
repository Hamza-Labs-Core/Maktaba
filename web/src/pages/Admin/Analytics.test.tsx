import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import { AdminAnalytics } from "./Analytics";
import { BarList, Heatmap, LineChart } from "../../components/charts/Charts";
import { renderPage, memberUser, quietCrash, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const A = "/api/admin/analytics";
const LIB = "11111111-1111-4111-8111-111111111111";
const SARA = "22222222-2222-4222-8222-222222222222";

const stat = (label: string, sessions: number, watch_sec: number, people = 1) => ({
  label,
  sessions,
  watch_sec,
  people,
});
const SUMMARY = {
  range: "7d",
  watch_sec: 9000,
  video_sec: 5400,
  tv_sec: 3600,
  live_tv_hours: 1,
  plays: 42,
  viewings: 7,
  people: 5,
  completion_rate: 0.634,
  surfaces: [
    stat("video", 42, 5400, 4),
    stat("app", 3, 1800),
    stat("tuner", 2, 1200, 0),
    stat("remote", 2, 600),
  ],
  apps: [stat("player", 30, 6000), stat("unknown", 12, 3000)],
  platforms: [stat("ios", 30, 6000)],
  devices: [stat("phone", 30, 6000)],
};
const heat = () => {
  const m = Array.from({ length: 7 }, () => Array.from({ length: 24 }, () => 0));
  m[2][20] = 3600;
  return m;
};
const ACT = {
  bucket: "day",
  series: Array.from({ length: 7 }, (_, i) => ({
    bucket: `2026-10-2${i}`,
    watch_sec: 600 * (i + 1),
    video_sec: 300 * (i + 1),
    tv_sec: 300 * (i + 1),
    plays: i,
    viewings: i,
  })),
  heatmap: heat(),
};
const NOW = {
  plays: [
    {
      session_id: "s1",
      user_id: SARA,
      username: "sara",
      video_id: "v1",
      title: "Omar E1",
      started_at: "2026-10-26T10:00:00Z",
      elapsed_sec: 1500,
      percent_complete: 41.6,
      client_app: "player",
      client_version: "1.3.0",
      platform: "ios",
      device_type: "phone",
    },
  ],
  viewings: [
    {
      id: "t1",
      surface: "tuner",
      channel_id: "c1",
      channel_name: "Quran",
      channel_number: 1,
      user_id: "",
      username: "",
      started_at: "2026-10-26T10:00:00Z",
      elapsed_sec: 600,
      client_app: "unknown",
      client_version: "",
      platform: "",
      device_type: "",
    },
    {
      id: "r1",
      surface: "remote",
      channel_id: "c2",
      channel_name: "News",
      channel_number: 2,
      user_id: "u3",
      username: "lina",
      started_at: "2026-10-26T10:00:00Z",
      elapsed_sec: 120,
      client_app: "unknown",
      client_version: "",
      platform: "",
      device_type: "tv",
    },
  ],
  counts: { video: 1, app: 0, tuner: 1, remote: 1 },
  at: "2026-10-26T10:25:00Z",
};
const TOP = {
  videos: [
    {
      video_id: "v1",
      title: "Omar E1",
      sessions: 10,
      watch_sec: 3600,
      unique_viewers: 3,
      completion_rate: 0.5,
    },
  ],
};
const LIBS = { libraries: [{ id: LIB, ...stat("Series", 40, 8000) }] };
const GENRES = { genres: [stat("Drama", 20, 5000)] };
const CHANNELS = {
  channels: [
    {
      channel_id: "c1",
      name: "Quran",
      number: 1,
      viewings: 3,
      watch_sec: 8340,
      people: 2,
      peak_viewers: 2,
      surfaces: [stat("app", 1, 3600), stat("remote", 1, 3540)],
    },
  ],
  surfaces: [stat("app", 2, 3900), stat("tuner", 2, 4200, 0), stat("remote", 1, 3540)],
  peak_viewers: 2,
  peak_at: "2026-10-26T12:30:00Z",
};
const REMOTE = {
  minutes_delivered: 180,
  simulcast_minutes: 10,
  cost_usd: 0.18,
  viewings: 1,
  watch_sec: 3540,
  people: 1,
  days: [{ day: "2026-10-25", minutes_delivered: 120, simulcast_minutes: 10, cost_usd: 0.12 }],
};
const PEOPLE = {
  people: [
    {
      user_id: SARA,
      username: "sara",
      watch_sec: 5700,
      video_sec: 2100,
      tv_sec: 3600,
      plays: 2,
      viewings: 1,
      channels: 1,
      apps: ["player", "tv", "unknown"],
      last_watched_at: "2026-10-26T14:55:00Z",
      last_active_day: "2026-10-26",
    },
  ],
};
const APPS = {
  apps: [
    {
      client_app: "player",
      people: 2,
      requests: 150,
      errors: 5,
      error_rate: 5 / 150,
      slow: 2,
      avg_ms: 83.3,
      versions: [
        { version: "1.3.0", people: 1 },
        { version: "1.2.0", people: 1 },
      ],
      platforms: [
        { platform: "ios", requests: 100, errors: 5, error_rate: 0.05, slow: 2, avg_ms: 100 },
        { platform: "android", requests: 50, errors: 0, error_rate: 0, slow: 0, avg_ms: 50 },
      ],
    },
  ],
  trend: [
    { day: "2026-10-25", requests: 50, errors: 0, people: 1 },
    { day: "2026-10-26", requests: 100, errors: 5, people: 2 },
  ],
  platforms: [stat("ios", 3, 2700)],
  devices: [stat("phone", 3, 2700)],
};

type Route = unknown | ((url: string) => unknown);
const DATA: Record<string, Route> = {
  summary: SUMMARY,
  activity: ACT,
  now: NOW,
  "top-videos": TOP,
  "top-channels": CHANNELS,
  libraries: LIBS,
  genres: GENRES,
  people: PEOPLE,
  apps: APPS,
  "remote-tv": REMOTE,
};
function route(over: Record<string, Route> = {}) {
  const table = { ...DATA, ...over };
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0]!;
    const name = path.startsWith(`${A}/`) ? path.slice(A.length + 1) : "";
    if (!(name in table)) return Promise.reject(new Error(`unmocked GET ${url}`));
    const v = table[name];
    try {
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    } catch (e) {
      return Promise.reject(e);
    }
  });
}
function open(over: Record<string, Route> = {}, user: User | null = adminUser) {
  route(over);
  return renderPage(<AdminAnalytics />, { path: "/admin/analytics", user });
}
const calls = (name: string) =>
  get.mock.calls.map((c) => c[0] as string).filter((u) => u.split("?")[0] === `${A}/${name}`);
const params = (url: string | undefined) => new URL(`http://x${url ?? ""}`).searchParams;
function kpi(label: string) {
  return screen
    .getByText(label, { selector: ".mkt-kpi__label" })
    .parentElement!.querySelector(".mkt-kpi__value")!.textContent;
}
const problem = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });
async function openTab(name: string) {
  await userEvent.setup().click(screen.getByRole("tab", { name }));
}
const NULLS: Record<string, Route> = {
  summary: { surfaces: null, apps: null, platforms: null, devices: null },
  activity: { series: null, heatmap: null },
  now: { plays: null, viewings: null, counts: null },
  "top-videos": { videos: null },
  "top-channels": { channels: null, surfaces: null },
  libraries: { libraries: null },
  genres: { genres: null },
  people: { people: null },
  apps: { apps: null, trend: null, platforms: null, devices: null },
  "remote-tv": { days: null },
};

describe("AdminAnalytics (test-cases/admin-analytics.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("ANL-01: Overview shows the numbers, both breakdowns, the trend and the heatmap", async () => {
    open();
    expect(screen.getByRole("heading", { level: 1, name: "Analytics" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Overview", selected: true })).toBeInTheDocument();
    await waitFor(() => expect(kpi("Watch time")).toBe("2h 30m"));
    expect(kpi("Plays")).toBe("42");
    expect(kpi("Live TV viewings")).toBe("7");
    expect(kpi("People watching")).toBe("5");
    expect(kpi("Live TV hours")).toBe("1");
    const surfaces = screen.getByRole("list", { name: "Watch time by way of watching" });
    for (const s of ["Videos in apps", "Live TV in apps", "Plex Live TV", "Remote TV"])
      expect(surfaces).toHaveTextContent(s);
    const apps = screen.getByRole("list", { name: "Watch time by app" });
    expect(apps).toHaveTextContent("player");
    expect(apps).toHaveTextContent("Unknown app");
    expect(await screen.findByRole("img", { name: "Watch time over time" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Peak viewing hours" })).toBeInTheDocument();
    expect(params(calls("summary")[0]).get("range")).toBe("7d");
    // Only the open tab asks: no Now, Videos, Live TV, People or Apps reads.
    expect(calls("now")).toHaveLength(0);
    expect(calls("top-videos")).toHaveLength(0);
    expect(calls("apps")).toHaveLength(0);
  });

  it("ANL-02: the range drives every call and the export links; custom sends from and to", async () => {
    open();
    await waitFor(() => expect(kpi("Plays")).toBe("42"));
    const user = userEvent.setup();
    for (const r of ["today", "30d", "90d"]) {
      await user.selectOptions(screen.getByLabelText("Range"), r);
      await waitFor(() => expect(params(calls("summary").at(-1)).get("range")).toBe(r));
      expect(params(calls("activity").at(-1)).get("range")).toBe(r);
      expect(screen.getByRole("link", { name: "Export CSV" })).toHaveAttribute(
        "href",
        `${A}/export?format=csv&range=${r}`
      );
      expect(screen.getByRole("link", { name: "Export JSON" })).toHaveAttribute(
        "href",
        `${A}/export?format=json&range=${r}`
      );
    }
    await user.selectOptions(screen.getByLabelText("Range"), "custom");
    const from = screen.getByLabelText("From");
    const to = screen.getByLabelText("To");
    expect((from as HTMLInputElement).value).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    await user.clear(from);
    await user.type(from, "2026-10-01");
    await user.clear(to);
    await user.type(to, "2026-10-03");
    await waitFor(() => {
      const p = params(calls("summary").at(-1));
      expect([p.get("from"), p.get("to"), p.get("range")]).toEqual([
        "2026-10-01",
        "2026-10-03",
        null,
      ]);
    });
    expect(screen.getByRole("link", { name: "Export CSV" })).toHaveAttribute(
      "href",
      `${A}/export?format=csv&from=2026-10-01&to=2026-10-03`
    );
  });

  it("ANL-03: the filters reach every call and the export; Clear filters resets them", async () => {
    open();
    const user = userEvent.setup();
    // The library and person lists come from the range's libraries and people.
    await screen.findByRole("option", { name: "Series" });
    await screen.findByRole("option", { name: "sara" });
    await user.selectOptions(screen.getByLabelText("Library"), LIB);
    await user.selectOptions(screen.getByLabelText("Person"), SARA);
    await user.selectOptions(screen.getByLabelText("App"), "player");
    await user.selectOptions(screen.getByLabelText("Way of watching"), "video");
    await waitFor(() => {
      const p = params(calls("summary").at(-1));
      expect([p.get("library"), p.get("user"), p.get("client_app"), p.get("surface")]).toEqual([
        LIB,
        SARA,
        "player",
        "video",
      ]);
    });
    expect(params(calls("activity").at(-1)).get("surface")).toBe("video");
    const csv = screen.getByRole("link", { name: "Export CSV" }).getAttribute("href")!;
    expect(params(csv).get("client_app")).toBe("player");
    expect(params(csv).get("user")).toBe(SARA);
    // The filter lists themselves are read for the range alone.
    expect(params(calls("people").at(-1)).get("surface")).toBeNull();

    await openTab("Apps");
    await waitFor(() => expect(params(calls("apps").at(-1)).get("client_app")).toBe("player"));

    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(params(calls("apps").at(-1)).get("client_app")).toBeNull());
    expect(screen.getByLabelText("Library")).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("ANL-04: Now lists plays and viewings with app, device and way of watching", async () => {
    open();
    await openTab("Now");
    const table = await screen.findByRole("table", { name: "Watching now" });
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(4);
    expect(rows[1]).toHaveTextContent("sara");
    expect(rows[1]).toHaveTextContent("Omar E1");
    expect(rows[1]).toHaveTextContent("Videos in apps");
    expect(rows[1]).toHaveTextContent("player 1.3.0");
    expect(rows[1]).toHaveTextContent("phone · ios");
    expect(rows[1]).toHaveTextContent("25m");
    expect(rows[1]).toHaveTextContent("42%");
    expect(rows[2]).toHaveTextContent("Plex (tuner)");
    expect(rows[2]).toHaveTextContent("1 Quran");
    expect(rows[2]).toHaveTextContent("Plex Live TV");
    expect(rows[2]).toHaveTextContent("Unknown app");
    expect(rows[3]).toHaveTextContent("lina");
    expect(rows[3]).toHaveTextContent("Remote TV");
    expect(kpi("Videos in apps")).toBe("1");
    expect(kpi("Live TV in apps")).toBe("0");
    expect(kpi("Remote TV")).toBe("1");
    expect(screen.getByText("Refreshes every 15 seconds.")).toBeInTheDocument();
  });

  it("ANL-05: Now asks every 15 s and keeps the last list when one ask fails", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    open({
      now: () => {
        n += 1;
        if (n === 2) throw problem(500, "internal");
        return n === 1 ? NOW : { plays: [], viewings: [], counts: {} };
      },
    });
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("tab", { name: "Now" }));
    await screen.findByRole("table", { name: "Watching now" });
    expect(calls("now")).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(calls("now")).toHaveLength(2);
    expect(screen.getByRole("table", { name: "Watching now" })).toHaveTextContent("sara");
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(calls("now")).toHaveLength(3);
    expect(await screen.findByText("Nobody is watching right now.")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ANL-06: Now with nobody watching shows the empty state", async () => {
    open({ now: { plays: [], viewings: [], counts: { video: 0 } } });
    await openTab("Now");
    expect(await screen.findByText("Nobody is watching right now.")).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Watching now" })).toBeNull();
    expect(kpi("Plex Live TV")).toBe("0");
  });

  it("ANL-07: Videos shows plays, completion, top videos, libraries and genres", async () => {
    open();
    await openTab("Videos");
    const table = await screen.findByRole("table", { name: "Most watched videos" });
    expect(within(table).getAllByRole("row")[1]).toHaveTextContent(/Omar E1.*10.*1h.*3.*50%/);
    await waitFor(() => expect(kpi("Completion rate")).toBe("63%"));
    expect(kpi("Video watch time")).toBe("1h 30m");
    expect(screen.getByRole("list", { name: "Libraries" })).toHaveTextContent("Series");
    expect(screen.getByRole("list", { name: "Genres" })).toHaveTextContent("Drama");
    expect(params(calls("top-videos")[0]).get("limit")).toBe("20");
  });

  it("ANL-08: Live TV shows channels with their peak and ways of watching, and the remote TV's cost", async () => {
    open();
    await openTab("Live TV");
    const table = await screen.findByRole("table", { name: "Most watched channels" });
    const row = within(table).getAllByRole("row")[1]!;
    expect(row).toHaveTextContent("1 Quran");
    expect(row).toHaveTextContent("2h 19m");
    expect(row).toHaveTextContent("Live TV in apps 1h · Remote TV 59m");
    await waitFor(() => expect(kpi("Peak viewers")).toBe("2"));
    expect(kpi("Remote TV minutes")).toBe("180");
    expect(kpi("Remote TV cost")).toBe("$0.18");
    expect(screen.getByText(/^Busiest at /)).toBeInTheDocument();
    const remote = screen.getByRole("table", { name: "Remote TV" });
    expect(remote).toHaveTextContent("2026-10-25");
    expect(remote).toHaveTextContent("$0.12");
    expect(screen.getByRole("list", { name: "Live TV by way of watching" })).toHaveTextContent(
      "Plex Live TV"
    );
    expect(screen.getByRole("img", { name: "Live TV over time" })).toBeInTheDocument();
  });

  it("ANL-09: People lists each person's watching, apps and last activity", async () => {
    open();
    await openTab("People");
    const table = await screen.findByRole("table", { name: "People" });
    const row = within(table).getAllByRole("row")[1]!;
    expect(row).toHaveTextContent("sara");
    expect(row).toHaveTextContent("1h 35m");
    expect(row).toHaveTextContent("player, tv, Unknown app");
    expect(row).toHaveTextContent("2026-10-26");
  });

  it("ANL-10: Apps shows each app's people, requests, errors, speed and versions, by platform", async () => {
    open();
    await openTab("Apps");
    const table = await screen.findByRole("table", { name: "Apps" });
    const row = within(table).getAllByRole("row")[1]!;
    expect(row).toHaveTextContent("player");
    expect(row).toHaveTextContent("150");
    expect(row).toHaveTextContent("3.3%");
    expect(row).toHaveTextContent("83 ms");
    expect(row).toHaveTextContent("1.3.0 (1), 1.2.0 (1)");
    const byPlatform = screen.getByRole("table", { name: "Apps by platform" });
    expect(within(byPlatform).getAllByRole("row")).toHaveLength(3);
    expect(byPlatform).toHaveTextContent("android");
    expect(kpi("Requests")).toBe("150");
    expect(kpi("Errors")).toBe("5");
    expect(kpi("People using apps")).toBe("2");
    expect(screen.getByRole("img", { name: "Requests per day" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Devices watched on" })).toHaveTextContent("phone");
  });

  it("ANL-10b: a tab the plan does not include shows its reason alone", async () => {
    open({
      apps: () => {
        throw new ApiError({
          type: "https://maktaba.tv/problems/license-required",
          title: "Not included in this home's plan",
          status: 403,
          detail:
            "Analytics older than 30 days, the per-user breakdown and export need the Home or Pro plan.",
        });
      },
    });
    await openTab("Apps");
    expect(await screen.findByRole("alert")).toHaveTextContent("Not included in this home's plan");
    expect(screen.queryByRole("table", { name: "Apps" })).not.toBeInTheDocument();
    expect(document.querySelector(".mkt-kpi")).toBeNull();
  });

  it("ANL-10c: Plex's Live TV is named, not an unknown app", async () => {
    open({
      summary: {
        ...(SUMMARY as Record<string, unknown>),
        apps: [{ label: "plex", sessions: 3, watch_sec: 7200, people: 0 }],
      },
    });
    expect(await screen.findByRole("list", { name: "Watch time by app" })).toHaveTextContent(
      "Plex (Live TV)"
    );
  });

  it.each([
    ["Videos", "No video was played in this range.", { "top-videos": { videos: [] } }],
    [
      "Live TV",
      "No channel was watched in this range.",
      { "top-channels": { channels: [], surfaces: [], peak_viewers: 0 } },
    ],
    ["Live TV", "The remote TV delivered nothing in this range.", { "remote-tv": { days: [] } }],
    ["People", "Nobody watched or used an app in this range.", { people: { people: [] } }],
    [
      "Apps",
      "No app called the server in this range.",
      { apps: { apps: [], trend: [], platforms: [], devices: [] } },
    ],
  ])("ANL-11: %s with nothing in the range says %s", async (tab, text, over) => {
    open(over);
    await openTab(tab);
    expect((await screen.findAllByText(text)).length).toBeGreaterThan(0);
  });

  it("ANL-11: Overview with nothing watched says so", async () => {
    open({
      summary: { ...SUMMARY, watch_sec: 0, plays: 0, viewings: 0, surfaces: [], apps: [] },
    });
    expect(await screen.findByText("Nothing was watched in this range.")).toBeInTheDocument();
  });

  it.each(["Overview", "Now", "Videos", "Live TV", "People", "Apps"])(
    "ANL-12: %s survives null and missing lists",
    async (tab) => {
      quietCrash();
      open(NULLS);
      if (tab !== "Overview") await openTab(tab);
      await waitFor(() => expect(screen.queryAllByText("Loading…")).toHaveLength(0));
      expect(screen.getByRole("tabpanel")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    }
  );

  it("ANL-13: one failing endpoint shows its message and the other cards still fill in", async () => {
    open({
      activity: () => {
        throw problem(500, "Internal error", "activity is down");
      },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(/activity is down|Internal error/);
    await waitFor(() => expect(kpi("Plays")).toBe("42"));
  });

  it("ANL-14: Refresh asks again, the summary skipping the cache", async () => {
    open();
    await waitFor(() => expect(kpi("Plays")).toBe("42"));
    await userEvent.setup().click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(calls("summary")).toHaveLength(2));
    expect(params(calls("summary")[1]).get("refresh")).toBe("true");
    expect(calls("activity")).toHaveLength(2);
  });

  it("ANL-15: a late answer for an old range does not win", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const later = (v: unknown) => new Promise((r) => setTimeout(() => r(v), 500));
    open({
      summary: (u: string) =>
        u.includes("7d") ? later({ ...SUMMARY, plays: 7 }) : { ...SUMMARY, plays: 30 },
    });
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .selectOptions(screen.getByLabelText("Range"), "30d");
    await waitFor(() => expect(kpi("Plays")).toBe("30"));
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(kpi("Plays")).toBe("30");
  });

  it("ANL-15: a card whose endpoint fails for the new range does not keep the old numbers", async () => {
    open({
      summary: (u: string) => {
        if (u.includes("30d")) throw problem(500, "Internal error");
        return SUMMARY;
      },
    });
    await waitFor(() => expect(kpi("Plays")).toBe("42"));
    await userEvent.setup().selectOptions(screen.getByLabelText("Range"), "30d");
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal error");
    expect(kpi("Plays")).toBe("—");
  });

  it("ANL-16: a non-admin sees Admins only and nothing is asked", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open({}, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(get).not.toHaveBeenCalled();
  });

  it("ANL-17: a signed-out user is sent to /login", () => {
    open({}, null);
    expect(screen.getByText("login page (from /admin/analytics)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ANL-18: Now stops asking when the tab or the page is left", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.click(screen.getByRole("tab", { name: "Now" }));
    await screen.findByRole("table", { name: "Watching now" });
    await user.click(screen.getByRole("tab", { name: "People" }));
    const before = calls("now").length;
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(calls("now")).toHaveLength(before);
    await user.click(screen.getByRole("tab", { name: "Now" }));
    await screen.findByRole("table", { name: "Watching now" });
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    const left = calls("now").length;
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(calls("now")).toHaveLength(left);
  });

  it("ANL-19: a render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    let boom = true;
    route({
      "top-videos": () => ({
        videos: [
          {
            video_id: "v",
            get title() {
              if (boom) throw new Error("boom");
              return "Omar E1";
            },
          },
        ],
      }),
    });
    renderPage(<AdminAnalytics />, { path: "/admin/analytics" });
    await openTab("Videos");
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    boom = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    await openTab("Videos");
    expect(await screen.findByRole("table", { name: "Most watched videos" })).toHaveTextContent(
      "Omar E1"
    );
  });

  it("ANL-20: in Arabic the page reads right to left with the Arabic words", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["analytics.title"] })
    ).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    for (const k of ["overview", "now", "videos", "livetv", "people", "apps"])
      expect(screen.getByRole("tab", { name: ar[`analytics.tab.${k}` as keyof typeof ar] }));
    expect(screen.getByLabelText(ar["analytics.filter.surface"])).toBeInTheDocument();
  });

  it("ANL-21: every page string is in both catalogs", () => {
    const keys = (c: Record<string, string>) =>
      Object.keys(c)
        .filter((k) => k.startsWith("analytics."))
        .sort();
    expect(keys(ar)).toEqual(keys(en));
    for (const k of keys(en)) expect((ar as Record<string, string>)[k], k).toBeTruthy();
  });

  it("ANL-22: tabs, controls, charts and tables are labelled", async () => {
    open();
    expect(screen.getByRole("tablist", { name: "Analytics sections" })).toBeInTheDocument();
    expect(screen.getAllByRole("tab")).toHaveLength(6);
    expect(screen.getByRole("group", { name: "Filters" })).toBeInTheDocument();
    for (const name of ["Range", "Library", "Person", "App", "Way of watching"])
      expect(screen.getByRole("combobox", { name })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
    expect(await screen.findByRole("img", { name: "Watch time over time" })).toBeInTheDocument();
    await openTab("Now");
    const now = await screen.findByRole("table", { name: "Watching now" });
    expect(within(now).getAllByRole("columnheader")).toHaveLength(7);
  });

  describe("ANL-23: chart maths", () => {
    it("LineChart: ticks every 5th of 30 points, top point at the top padding, finite coords", () => {
      const points = Array.from({ length: 30 }, (_, i) => ({ label: `d${i}`, value: i * 100 }));
      const { container } = render(<LineChart points={points} ariaLabel="c" />);
      expect(container.querySelectorAll(".mkt-chart__tick")).toHaveLength(6);
      const d = container.querySelector(".mkt-chart__line")!.getAttribute("d")!;
      expect(d).not.toMatch(/NaN|Infinity/);
      expect(d.split(" ").at(-1)).toMatch(/,8$/);
    });

    it("BarList: widths are relative to the max; sub text shown", () => {
      const { container } = render(
        <BarList
          ariaLabel="b"
          items={[
            { label: "a", value: 50, sub: "fifty" },
            { label: "b", value: 100 },
            { label: "c", value: 0 },
          ]}
        />
      );
      const widths = Array.from(container.querySelectorAll<HTMLElement>(".mkt-barlist__fill")).map(
        (e) => e.style.width
      );
      expect(widths).toEqual(["50%", "100%", "0%"]);
      expect(container).toHaveTextContent("fifty");
    });

    // Charts.tsx (src/components/charts) does not clamp; see the report.
    it.fails("BarList: a negative value never gives a negative width", () => {
      const { container } = render(
        <BarList
          ariaLabel="b"
          items={[
            { label: "a", value: -5 },
            { label: "b", value: 100 },
          ]}
        />
      );
      const w = container.querySelector<HTMLElement>(".mkt-barlist__fill")!.style.width;
      expect(w).toBe("0%");
    });

    it("Heatmap: opacity scales with the value; titles name day and hour", () => {
      const m = Array.from({ length: 7 }, () => Array.from({ length: 24 }, () => 0));
      m[0][0] = 100;
      m[0][1] = 50;
      const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
      const { container } = render(<Heatmap matrix={m} dayLabels={days} ariaLabel="h" />);
      const cells = container.querySelectorAll<HTMLElement>(".mkt-heatmap__cell");
      expect(cells[0].style.opacity).toBe("1");
      expect(cells[1].style.opacity).toBe("0.575");
      expect(cells[2].style.opacity).toBe("0.06");
      expect(cells[0]).toHaveAttribute("title", "Sun 0:00 — 100s");
    });
  });
});
