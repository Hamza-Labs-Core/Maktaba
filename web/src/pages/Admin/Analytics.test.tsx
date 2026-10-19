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

const A = "/api/admin/analytics";
const SUMMARY = {
  range: "7d",
  total_watch_sec: 9000,
  total_sessions: 42,
  unique_viewers: 5,
  completion_rate: 0.634,
  devices: [
    { label: "web", sessions: 30, watch_sec: 6000 },
    { label: "mobile", sessions: 12, watch_sec: 3000 },
  ],
  platforms: [],
  libraries: [{ id: "l1", label: "Series", sessions: 40, watch_sec: 8000 }],
  genres: [{ label: "Drama", sessions: 20, watch_sec: 5000 }],
};
const TOP = {
  range: "7d",
  videos: [{ video_id: "v1", title: "Omar E1", sessions: 10, watch_sec: 3600, unique_viewers: 3 }],
};
const heat = () => {
  const m = Array.from({ length: 7 }, () => Array.from({ length: 24 }, () => 0));
  m[2][20] = 3600;
  return m;
};
const ACT = {
  bucket: "day",
  series: Array.from({ length: 7 }, (_, i) => ({
    bucket: `2026-10-1${i + 3}`,
    watch_sec: 600 * (i + 1),
    sessions: i,
  })),
  heatmap: heat(),
};
const USERS = {
  range: "7d",
  users: [
    {
      user_id: "u1",
      username: "sara",
      watch_sec: 5400,
      sessions: 9,
      last_seen_at: "2026-10-18T20:00:00Z",
    },
  ],
};
const LIVE = {
  sessions: [
    {
      session_id: "s1",
      user_id: "u1",
      username: "sara",
      video_id: "v1",
      title: "Omar E1",
      started_at: "2026-10-19T10:00:00Z",
      elapsed_sec: 1500,
      percent_complete: 41.6,
      device_type: "web",
    },
  ],
};

type Route = unknown | ((url: string) => unknown);
interface Data {
  summary?: Route;
  top?: Route;
  activity?: Route;
  users?: Route;
  live?: Route;
}
function route(d: Data = {}) {
  const table: Record<string, Route> = {
    [`${A}/summary`]: d.summary ?? SUMMARY,
    [`${A}/top-videos`]: d.top ?? TOP,
    [`${A}/activity`]: d.activity ?? ACT,
    [`${A}/users`]: d.users ?? USERS,
    [`${A}/live`]: d.live ?? LIVE,
  };
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0];
    if (!(path in table)) return Promise.reject(new Error(`unmocked GET ${url}`));
    const v = table[path];
    try {
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    } catch (e) {
      return Promise.reject(e);
    }
  });
}
function open(d: Data = {}, user: User | null = adminUser) {
  route(d);
  return renderPage(<AdminAnalytics />, { path: "/admin/analytics", user });
}
const calls = (part: string) =>
  get.mock.calls.map((c) => c[0] as string).filter((u) => u.includes(part));
function kpi(label: string) {
  return screen.getByText(label).parentElement!.querySelector(".mkt-kpi__value")!.textContent;
}
const problem = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });

describe("AdminAnalytics (test-cases/admin-analytics.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("ANL-01: dashboard renders all cards from full data", async () => {
    open();
    expect(screen.getByRole("heading", { level: 1, name: "Watch analytics" })).toBeInTheDocument();
    expect(screen.getByLabelText("Range")).toHaveValue("7d");
    expect(screen.getByRole("option", { name: "Last 7 days", selected: true })).toBeInTheDocument();
    await waitFor(() => expect(kpi("Watch time")).toBe("2h 30m"));
    expect(kpi("Sessions")).toBe("42");
    expect(kpi("Unique viewers")).toBe("5");
    expect(kpi("Completion rate")).toBe("63%");

    const live = await screen.findByRole("table", { name: "Currently watching" });
    expect(
      within(live)
        .getAllByRole("columnheader")
        .map((h) => h.textContent)
    ).toEqual(["User", "Video", "Elapsed", "Progress", "Device"]);
    expect(
      within(live)
        .getAllByRole("cell")
        .map((c) => c.textContent)
    ).toEqual(["sara", "Omar E1", "25m", "42%", "web"]);

    const line = screen.getByRole("img", { name: "Watch time over time" });
    expect(line.querySelector("path.mkt-chart__line")).not.toBeNull();
    const hm = screen.getByRole("img", { name: "Peak viewing hours" });
    const rows = hm.querySelectorAll(".mkt-heatmap__row");
    expect(Array.from(rows).map((r) => r.querySelector(".mkt-heatmap__day")!.textContent)).toEqual([
      "Sun",
      "Mon",
      "Tue",
      "Wed",
      "Thu",
      "Fri",
      "Sat",
    ]);
    for (const r of Array.from(rows))
      expect(r.querySelectorAll(".mkt-heatmap__cell")).toHaveLength(24);

    const bars = (name: string) =>
      within(screen.getByRole("list", { name }))
        .getAllByRole("listitem")
        .map((li) => li.textContent);
    expect(bars("Most watched")).toEqual(["Omar E110 · 1h"]);
    expect(bars("Most active users")).toEqual(["sara1h 30m"]);
    expect(bars("Popular genres")).toEqual(["Drama20"]);
    expect(bars("Devices")).toEqual(["web30", "mobile12"]);
    expect(bars("Library usage")).toEqual(["Series2h 13m"]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ANL-02: range change refetches the four endpoints and updates export links", async () => {
    open();
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(calls("/summary")).toEqual([`${A}/summary?range=7d`]);
    expect(calls("/top-videos")[0]).toMatch(/range=7d/);
    expect(calls("/top-videos")[0]).toMatch(/limit=10/);
    expect(calls("/activity")[0]).toMatch(/bucket=day/);
    expect(calls("/users")[0]).toMatch(/limit=10/);
    const user = userEvent.setup();
    for (const r of ["today", "30d", "90d", "1y", "all"]) {
      await user.selectOptions(screen.getByLabelText("Range"), r);
      await waitFor(() => expect(calls("/summary").at(-1)).toBe(`${A}/summary?range=${r}`));
      expect(calls("/top-videos").at(-1)).toMatch(new RegExp(`range=${r}`));
      expect(calls("/activity").at(-1)).toMatch(
        new RegExp(`range=${r}.*bucket=day|bucket=day.*range=${r}`)
      );
      expect(calls("/users").at(-1)).toMatch(new RegExp(`range=${r}`));
      expect(screen.getByRole("link", { name: "Export CSV" })).toHaveAttribute(
        "href",
        `${A}/export?format=csv&range=${r}`
      );
      expect(screen.getByRole("link", { name: "Export JSON" })).toHaveAttribute(
        "href",
        `${A}/export?format=json&range=${r}`
      );
    }
    expect(calls("/summary")).toHaveLength(6);
    expect(calls("/live")).toHaveLength(1);
  });

  it("ANL-03: live polls every 15 s and keeps the last value on error", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    open({
      live: () => {
        n += 1;
        if (n === 2) throw problem(500, "internal");
        return n === 1 ? LIVE : { sessions: [] };
      },
    });
    await screen.findByRole("table", { name: "Currently watching" });
    expect(calls("/live")).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(calls("/live")).toHaveLength(2);
    expect(screen.getByRole("table", { name: "Currently watching" })).toHaveTextContent("sara");
    expect(screen.queryByRole("alert")).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(calls("/live")).toHaveLength(3);
    expect(await screen.findByText("Nobody is watching right now.")).toBeInTheDocument();
    expect(calls("/summary")).toHaveLength(1);
  });

  it("ANL-04: live sessions [] shows Nobody is watching right now.", async () => {
    open({ live: { sessions: [] } });
    expect(await screen.findByText("Nobody is watching right now.")).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Currently watching" })).toBeNull();
  });

  it.each([
    ["sessions: null", { sessions: null }],
    ["sessions missing", {}],
  ])("ANL-05: live %s shows the empty state and the rest renders", async (_n, live) => {
    quietCrash();
    open({ live });
    expect(await screen.findByText("Nobody is watching right now.")).toBeInTheDocument();
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ANL-05: a live row with null fields renders blanks, 0m, 0%", async () => {
    open({
      live: {
        sessions: [
          {
            session_id: "s1",
            username: null,
            title: null,
            elapsed_sec: null,
            percent_complete: null,
          },
        ],
      },
    });
    const live = await screen.findByRole("table", { name: "Currently watching" });
    expect(
      within(live)
        .getAllByRole("cell")
        .map((c) => c.textContent)
    ).toEqual(["", "", "0m", "0%", "—"]);
  });

  it("ANL-06: empty series and lists render placeholders", async () => {
    open({
      summary: {
        ...SUMMARY,
        total_watch_sec: 0,
        total_sessions: 0,
        unique_viewers: 0,
        completion_rate: 0,
        devices: [],
        platforms: [],
        libraries: [],
        genres: [],
      },
      top: { videos: [] },
      users: { users: [] },
      activity: {
        bucket: "day",
        series: [],
        heatmap: Array.from({ length: 7 }, () => Array.from({ length: 24 }, () => 0)),
      },
    });
    await waitFor(() => expect(kpi("Watch time")).toBe("0m"));
    expect(kpi("Sessions")).toBe("0");
    expect(kpi("Unique viewers")).toBe("0");
    expect(kpi("Completion rate")).toBe("0%");
    const line = screen.getByRole("img", { name: "Watch time over time" });
    expect(line.querySelector("path")).toBeNull();
    expect(line).toHaveTextContent("—");
    expect(screen.queryAllByRole("list")).toHaveLength(0);
    const cells = Array.from(
      screen
        .getByRole("img", { name: "Peak viewing hours" })
        .querySelectorAll<HTMLElement>(".mkt-heatmap__cell")
    );
    expect(cells).toHaveLength(168);
    expect(cells.every((c) => c.style.opacity === "0.06")).toBe(true);
  });

  const nulls = (v: null | undefined) => ({
    summary: { ...SUMMARY, devices: v, genres: v, libraries: v, platforms: v },
    top: { videos: v },
    users: { users: v },
  });
  it.each([
    ["null", nulls(null)],
    ["missing", nulls(undefined)],
  ])("ANL-07: list fields %s show — and the rest renders", async (_n, d) => {
    quietCrash();
    open(d);
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(screen.queryAllByRole("list")).toHaveLength(0);
    expect(screen.getByRole("img", { name: "Peak viewing hours" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ANL-07: items with null labels render without throwing or key warnings", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    open({
      summary: {
        ...SUMMARY,
        genres: [{ label: null, sessions: 1, watch_sec: 1 }],
        devices: [null],
      },
      top: { videos: [{ video_id: "v", title: null, sessions: 1, watch_sec: 60 }] },
      users: { users: [{ user_id: "u", username: null, watch_sec: 60, sessions: 1 }] },
    });
    expect(await screen.findByRole("list", { name: "Popular genres" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Most watched" })).toHaveTextContent("1 · 1m");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(errors).not.toHaveBeenCalled();
  });

  it.each([
    ["heatmap: null", { series: [], heatmap: null }],
    ["heatmap missing", { series: [] }],
    ["heatmap []", { series: [], heatmap: [] }],
  ])("ANL-08: activity %s shows a placeholder and the rest renders", async (_n, activity) => {
    quietCrash();
    open({ activity });
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(screen.queryByRole("img", { name: "Peak viewing hours" })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    const peak = screen.getByRole("heading", { level: 2, name: "Peak viewing hours" });
    expect(peak.closest(".mk-card")).toHaveTextContent("—");
  });

  it("ANL-08: a null heatmap row and 12-value rows do not crash", async () => {
    quietCrash();
    const m: (number[] | null)[] = Array.from({ length: 7 }, () =>
      Array.from({ length: 12 }, () => 1)
    );
    m[3] = null;
    open({ activity: { series: [], heatmap: m } });
    const hm = await screen.findByRole("img", { name: "Peak viewing hours" });
    const rows = hm.querySelectorAll(".mkt-heatmap__row");
    expect(rows).toHaveLength(7);
    expect(rows[0].querySelectorAll(".mkt-heatmap__cell")).toHaveLength(12);
    expect(rows[3].querySelectorAll(".mkt-heatmap__cell")).toHaveLength(0);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["series: null", { series: null, heatmap: heat() }, "placeholder"],
    ["series missing", { heatmap: heat() }, "placeholder"],
    ["one point", { series: [{ bucket: "2026-10-19", watch_sec: 300 }], heatmap: heat() }, "line"],
    [
      "null values",
      {
        series: [
          { bucket: null, watch_sec: null },
          { bucket: "b", watch_sec: 10 },
        ],
        heatmap: heat(),
      },
      "line",
    ],
    [
      "all zero",
      {
        series: [
          { bucket: "a", watch_sec: 0 },
          { bucket: "b", watch_sec: 0 },
        ],
        heatmap: heat(),
      },
      "line",
    ],
  ])("ANL-09: activity %s", async (_n, activity, kind) => {
    open({ activity });
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    const line = screen.getByRole("img", { name: "Watch time over time" });
    if (kind === "placeholder") {
      expect(line.querySelector("path")).toBeNull();
      expect(line).toHaveTextContent("—");
    } else {
      for (const p of Array.from(line.querySelectorAll("path"))) {
        expect(p.getAttribute("d")).not.toMatch(/NaN/);
      }
    }
  });

  it.each([
    [
      "null",
      {
        ...SUMMARY,
        total_watch_sec: null,
        total_sessions: null,
        unique_viewers: null,
        completion_rate: null,
      },
    ],
    ["missing", { devices: [], platforms: [], libraries: [], genres: [] }],
  ])("ANL-10: summary numbers %s show 0, never null/NaN", async (_n, summary) => {
    open({ summary });
    await waitFor(() => expect(kpi("Watch time")).toBe("0m"));
    expect(kpi("Sessions")).toBe("0");
    expect(kpi("Unique viewers")).toBe("0");
    expect(kpi("Completion rate")).toBe("0%");
    expect(document.body.textContent).not.toMatch(/NaN|null|undefined/);
  });

  it.each([
    ["500", problem(500, "Internal error", "db down"), "Internal error: db down"],
    ["403", problem(403, "Forbidden"), "Forbidden"],
    ["network", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("ANL-12: top-videos fails (%s): alert, other cards still render", async (_n, err, text) => {
    open({
      top: () => {
        throw err;
      },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(text);
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(screen.getByRole("list", { name: "Most active users" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Peak viewing hours" })).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Most watched" })).toBeNull();
    expect(await screen.findByRole("table", { name: "Currently watching" })).toBeInTheDocument();
  });

  it("ANL-13: malformed JSON shows a localised alert; the page stays usable", async () => {
    open({
      summary: () => {
        throw new SyntaxError("Unexpected token b in JSON at position 1");
      },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong.");
    expect(screen.getByLabelText("Range")).toBeEnabled();
    expect(await screen.findByRole("list", { name: "Most watched" })).toBeInTheDocument();
  });

  it("ANL-13: an HTML string for activity does not crash", async () => {
    quietCrash();
    open({ activity: "<!doctype html><html></html>" });
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("img", { name: "Watch time over time" })).toHaveTextContent("—");
  });

  it("ANL-14: Try again clears the error and refetches", async () => {
    let n = 0;
    open({
      summary: () => {
        n += 1;
        if (n === 1) throw problem(500, "Internal error");
        return SUMMARY;
      },
    });
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    await waitFor(() => expect(kpi("Sessions")).toBe("42"));
    expect(calls("/summary")).toEqual([`${A}/summary?range=7d`, `${A}/summary?range=7d`]);
  });

  it("ANL-15: a late response for an old range does not win", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const later = (v: unknown) => new Promise((r) => setTimeout(() => r(v), 500));
    open({
      summary: (u: string) =>
        u.includes("7d")
          ? later({ ...SUMMARY, total_sessions: 7 })
          : { ...SUMMARY, total_sessions: 30 },
    });
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .selectOptions(screen.getByLabelText("Range"), "30d");
    await waitFor(() => expect(kpi("Sessions")).toBe("30"));
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(kpi("Sessions")).toBe("30");
  });

  it("ANL-17: non-admin sees Admins only, no requests, no polling", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open({}, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(get).not.toHaveBeenCalled();
  });

  it("ANL-18: signed-out user is redirected to /login", () => {
    open({}, null);
    expect(screen.getByText("login page (from /admin/analytics)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ANL-19: polling stops when leaving; Back starts exactly one interval", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open();
    await screen.findByRole("table", { name: "Currently watching" });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    const before = calls("/live").length;
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(calls("/live")).toHaveLength(before);
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByRole("table", { name: "Currently watching" });
    const back = calls("/live").length;
    expect(back).toBe(before + 1);
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(calls("/live")).toHaveLength(back + 1);
  });

  it("ANL-21: a render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    let title: unknown = { bad: 1 };
    route({ top: () => ({ videos: [{ video_id: "v", title, sessions: 1, watch_sec: 1 }] }) });
    renderPage(<AdminAnalytics />, { path: "/admin/analytics" });
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    title = "Omar E1";
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("list", { name: "Most watched" })).toHaveTextContent("Omar E1");
  });

  describe("ANL-22: chart maths", () => {
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

  it("ANL-26: labelled select, charts as img, labelled lists and table", async () => {
    open({
      summary: () => {
        throw problem(500, "Internal error");
      },
    });
    expect(screen.getByRole("combobox", { name: "Range" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Export CSV" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Export JSON" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Watch time over time" })).toBeInTheDocument();
    expect(await screen.findByRole("img", { name: "Peak viewing hours" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Most watched" })).toBeInTheDocument();
    const live = await screen.findByRole("table", { name: "Currently watching" });
    expect(within(live).getAllByRole("columnheader")).toHaveLength(5);
  });
});
