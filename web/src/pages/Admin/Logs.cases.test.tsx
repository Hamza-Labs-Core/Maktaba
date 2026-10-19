// Admin logs cases from web/test-cases/admin-logs.md (Logs.test.tsx mocks
// lib/auth, so these live in their own file on the shared page harness).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, downloadBlob, badge } = vi.hoisted(() => ({
  get: vi.fn(),
  downloadBlob: vi.fn(),
  badge: { throws: false },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn() },
    downloadBlob,
  };
});
// LOG-22 makes a row throw while rendering through the Badge.
vi.mock("@ds/components/Badge/Badge", async () => {
  const actual = await vi.importActual<typeof import("@ds/components/Badge/Badge")>(
    "@ds/components/Badge/Badge"
  );
  return {
    ...actual,
    Badge: (props: Parameters<typeof actual.Badge>[0]) => {
      if (badge.throws) throw new Error("badge exploded");
      return actual.Badge(props);
    },
  };
});

import { ApiError } from "../../lib/api";
import { AdminLogs } from "./Logs";

const STREAM = "/api/admin/logs/stream";
const CRASH = "This page hit an error and could not be shown.";
const EMPTY = "No log entries match the current filters.";

const two = {
  entries: [
    { ts: "2026-06-10T00:00:00Z", level: "info", service: "api", msg: "started" },
    { ts: "2026-06-10T00:00:01Z", level: "error", service: "streaming", msg: "boom", err: "x" },
  ],
  count: 2,
};

const open = (user = undefined as undefined | typeof memberUser | null) =>
  renderPage(
    <ToastProvider>
      <AdminLogs />
    </ToastProvider>,
    { path: "/admin/logs", ...(user !== undefined ? { user } : {}) }
  );

const rows = () => Array.from(document.querySelectorAll<HTMLLIElement>("li.mkt-logs__row"));
const streamCalls = () =>
  get.mock.calls.map(([u]) => String(u)).filter((u) => u.startsWith(STREAM));
const lastQuery = () => new URLSearchParams(streamCalls().at(-1)!.split("?")[1]);
const apiErr = (status: number, title = "x") =>
  new ApiError({ type: "about:blank", title, status });

describe("AdminLogs (test-cases/admin-logs.md)", () => {
  beforeEach(() => {
    get.mockReset();
    downloadBlob.mockReset();
    badge.throws = false;
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("LOG-01: renders entries with time, level badge, service and message", async () => {
    routeGet(get, { [STREAM]: two });
    open();
    expect(await screen.findByText("started")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Logs" })).toBeInTheDocument();
    expect(
      screen.getByText("Live service logs. Filter, search, and export a diagnostics bundle.")
    ).toBeInTheDocument();
    expect(streamCalls()[0]).toBe(`${STREAM}?level=debug&limit=1000`);
    const [r1, r2] = rows();
    expect(rows()).toHaveLength(2);
    expect(r1).toHaveClass("mkt-logs__row--accent");
    expect(r2).toHaveClass("mkt-logs__row--error");
    expect(r1.querySelector(".mkt-logs__ts")?.textContent).toMatch(/2026/);
    expect(within(r1).getByText("info")).toBeInTheDocument();
    expect(within(r1).getByText("api")).toBeInTheDocument();
    expect(within(r2).getByText("error")).toBeInTheDocument();
    expect(within(r2).getByText("streaming")).toBeInTheDocument();
    expect(within(r2).getByText("boom")).toBeInTheDocument();
    expect(r1.querySelector("details")).toBeNull();
    expect(r2.querySelector("details")).not.toBeNull();
  });

  it("LOG-02: non-admin sees Admins only; no log request", async () => {
    routeGet(get, { [STREAM]: two });
    open(memberUser);
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Export bundle" })).toBeNull();
    expect(get).not.toHaveBeenCalled();
    expect(downloadBlob).not.toHaveBeenCalled();
  });

  it("LOG-03: signed-out user is sent to /login", async () => {
    open(null);
    expect(await screen.findByText("login page (from /admin/logs)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("LOG-04: empty list shows the empty text and only All services", async () => {
    routeGet(get, { [STREAM]: { entries: [], count: 0 } });
    open();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(await screen.findByText(EMPTY)).toBeInTheDocument();
    const svc = screen.getByLabelText("Service");
    expect(
      within(svc)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All services"]);
  });

  it.each([
    ["entries null", { entries: null, count: 0 }],
    ["entries missing", { count: 0 }],
    ["body null", null],
  ])("LOG-05: %s renders the empty state, no error", async (_, body) => {
    routeGet(get, { [STREAM]: body });
    open();
    await waitFor(() => expect(get).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.getByText(EMPTY)).toBeInTheDocument();
    expect(screen.queryByText("Could not load logs.")).toBeNull();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("LOG-06: a null or non-object entry does not hide the other lines", async () => {
    routeGet(get, {
      [STREAM]: { entries: [{ level: "info", msg: "ok" }, null, "plain text line", 42], count: 4 },
    });
    open();
    expect(await screen.findByText("ok")).toBeInTheDocument();
    expect(screen.getByText("plain text line")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(rows()).toHaveLength(3);
    expect(screen.queryByText("Could not load logs.")).toBeNull();
    // a string entry is not split into one attr per character
    expect(rows()[1].querySelector("details")).toBeNull();
  });

  it("LOG-07: entries missing ts / level / service / msg", async () => {
    routeGet(get, {
      [STREAM]: {
        entries: [
          {},
          { ts: "garbage", level: "INFO" },
          { msg: { nested: true } },
          { ts: 1760000000, level: 3 },
        ],
      },
    });
    open();
    await waitFor(() => expect(rows()).toHaveLength(4));
    const [a, b, c, d] = rows();
    expect(a.querySelector(".mkt-logs__ts")).toHaveTextContent("—");
    expect(within(a).getByText("?")).toBeInTheDocument();
    expect(a.querySelector(".mkt-logs__service")).toBeNull();
    expect(a.querySelector(".mkt-logs__msg")).toHaveTextContent("");
    expect(b.querySelector(".mkt-logs__ts")).toHaveTextContent("garbage");
    expect(within(b).getByText("INFO")).toBeInTheDocument();
    expect(b).toHaveClass("mkt-logs__row--accent");
    expect(c.querySelector(".mkt-logs__msg")).toHaveTextContent('{"nested":true}');
    expect(d.querySelector(".mkt-logs__ts")?.textContent).toMatch(/2025/);
    expect(within(d).getByText("3")).toBeInTheDocument();
  });

  it.each([
    ["500", apiErr(500)],
    ["403", apiErr(403)],
    ["401", apiErr(401)],
    ["network", new TypeError("Failed to fetch")],
  ])("LOG-08: a failing poll (%s) shows the error and keeps old lines", async (_, err) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    routeGet(get, { [STREAM]: () => (n++ === 0 ? two : Promise.reject(err)) });
    open();
    expect(await screen.findByText("started")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(await screen.findByText("Could not load logs.")).toBeInTheDocument();
    expect(rows()).toHaveLength(2);
    const before = streamCalls().length;
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(streamCalls().length).toBe(before + 1);
  });

  it("LOG-08: a first-load failure shows the error and the empty text", async () => {
    routeGet(get, { [STREAM]: () => Promise.reject(apiErr(500)) });
    open();
    expect(await screen.findByText("Could not load logs.")).toBeInTheDocument();
    expect(screen.getByText(EMPTY)).toBeInTheDocument();
  });

  it("LOG-09: recovers on the next poll after an error", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    routeGet(get, { [STREAM]: () => (n++ === 0 ? Promise.reject(apiErr(500)) : two) });
    open();
    expect(await screen.findByText("Could not load logs.")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(await screen.findByText("started")).toBeInTheDocument();
    expect(screen.queryByText("Could not load logs.")).toBeNull();
  });

  it("LOG-10: level filter changes the query and refetches", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    routeGet(get, { [STREAM]: two });
    open();
    await screen.findByText("started");
    const level = screen.getByLabelText("Level");
    expect(level).toHaveValue("debug");
    expect(
      within(level)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Debug", "Info", "Warning", "Error"]);
    const before = streamCalls().length;
    await user.selectOptions(level, "error");
    await waitFor(() => expect(streamCalls().length).toBe(before + 1));
    expect(lastQuery().get("level")).toBe("error");
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(lastQuery().get("level")).toBe("error");
    await user.selectOptions(level, "warn");
    await waitFor(() => expect(lastQuery().get("level")).toBe("warn"));
  });

  it("LOG-11: service options come from the current lines", async () => {
    const user = userEvent.setup();
    routeGet(get, {
      [STREAM]: (url: string) =>
        url.includes("services=api")
          ? { entries: [{ service: "api", msg: "only api" }] }
          : {
              entries: [
                { service: "streaming", msg: "s" },
                { service: "api", msg: "a1" },
                { service: "api", msg: "a2" },
                { service: "", msg: "blank" },
              ],
            },
    });
    open();
    await screen.findByText("blank");
    const svc = screen.getByLabelText("Service");
    expect(
      within(svc)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All services", "api", "streaming"]);
    await user.selectOptions(svc, "api");
    await screen.findByText("only api");
    expect(lastQuery().get("services")).toBe("api");
    expect(
      within(svc)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All services", "api"]);
  });

  it("LOG-12: search sends a trimmed, encoded q; whitespace sends none", async () => {
    const user = userEvent.setup();
    routeGet(get, { [STREAM]: two });
    open();
    await screen.findByText("started");
    const search = screen.getByLabelText("Search");
    expect(search).toHaveAttribute("placeholder", "Filter by text…");
    await user.type(search, " timeout ");
    await waitFor(() => expect(lastQuery().get("q")).toBe("timeout"));
    await user.clear(search);
    await user.type(search, "   ");
    await waitFor(() => expect(streamCalls().at(-1)).not.toContain("q="));
    await user.clear(search);
    await user.type(search, "خطأ");
    await waitFor(() => expect(streamCalls().at(-1)).toContain("q=%D8%AE%D8%B7%D8%A3"));
    await user.clear(search);
    await user.type(search, "a&b#c");
    await waitFor(() => expect(streamCalls().at(-1)).toContain("q=a%26b%23c"));
  });

  it("LOG-13: polls every 3 s; Pause stops, Resume restarts, unmount stops", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    routeGet(get, { [STREAM]: two });
    const { unmount } = open();
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(streamCalls()).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(9_000));
    expect(streamCalls()).toHaveLength(4);
    await act(async () => {
      screen.getByRole("button", { name: "Pause" }).click();
    });
    expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
    expect(streamCalls()).toHaveLength(5);
    await act(() => vi.advanceTimersByTimeAsync(9_000));
    expect(streamCalls()).toHaveLength(5);
    await act(async () => {
      screen.getByRole("button", { name: "Resume" }).click();
    });
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(streamCalls()).toHaveLength(7);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(9_000));
    expect(streamCalls()).toHaveLength(7);
  });

  it("LOG-15: extra fields open in the details row", async () => {
    const user = userEvent.setup();
    routeGet(get, {
      [STREAM]: {
        entries: [
          {
            ts: "2026-06-10T00:00:00Z",
            level: "error",
            service: "api",
            msg: "boom",
            version: "1.5",
            env: "prod",
            err: "x",
            req_id: "r1",
          },
        ],
      },
    });
    open();
    await screen.findByText("boom");
    const details = rows()[0].querySelector("details")!;
    await user.click(details.querySelector("summary")!);
    expect(details.open).toBe(true);
    const pre = details.querySelector("pre")!;
    expect(JSON.parse(pre.textContent!)).toEqual({ err: "x", req_id: "r1" });
    expect(pre.textContent).toContain('\n  "err": "x"');
  });

  it("LOG-18: export shows a busy button, then a success toast", async () => {
    const user = userEvent.setup();
    let finish: () => void = () => {};
    downloadBlob.mockImplementation(
      () =>
        new Promise<void>((r) => {
          finish = r;
        })
    );
    routeGet(get, { [STREAM]: two });
    open();
    await screen.findByText("started");
    await user.click(screen.getByRole("button", { name: "Export bundle" }));
    const busy = screen.getByRole("button", { name: "Exporting…" });
    expect(busy).toBeDisabled();
    await user.click(busy);
    expect(downloadBlob).toHaveBeenCalledTimes(1);
    await act(async () => finish());
    expect(await screen.findByText("Diagnostics bundle downloaded.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export bundle" })).toBeEnabled();
  });

  it.each([
    [
      "ApiError",
      new ApiError({ type: "about:blank", title: "Forbidden", status: 403 }),
      "Forbidden",
    ],
    ["network", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("LOG-19: export failure (%s) shows an error toast", async (_, err, detail) => {
    const user = userEvent.setup();
    downloadBlob.mockRejectedValue(err);
    routeGet(get, { [STREAM]: two });
    open();
    await screen.findByText("started");
    await user.click(screen.getByRole("button", { name: "Export bundle" }));
    expect(await screen.findByText(`Export failed: ${detail}`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export bundle" })).toBeEnabled();
  });

  it("LOG-22: a render crash shows the route error fallback; Back works; polling stops", async () => {
    const user = userEvent.setup();
    quietCrash();
    badge.throws = true;
    routeGet(get, { [STREAM]: two });
    open();
    expect(await screen.findByText(CRASH)).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    badge.throws = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("started")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("LOG-23: filters are not kept in the URL; Back returns with the default level", async () => {
    const user = userEvent.setup();
    routeGet(get, { [STREAM]: two });
    open();
    await screen.findByText("started");
    await user.selectOptions(screen.getByLabelText("Level"), "error");
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByLabelText("Level")).toHaveValue("debug");
  });

  it("LOG-24: Arabic UI keeps Arabic log text", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routeGet(get, {
      [STREAM]: {
        entries: [
          { level: "info", service: "api", msg: "تم تحميل المكتبة: ١٢ ملف" },
          { msg: "فشل GET /api/videos/123 بعد 3s" },
        ],
      },
    });
    open();
    expect(await screen.findByText("تم تحميل المكتبة: ١٢ ملف")).toBeInTheDocument();
    expect(screen.getByText("فشل GET /api/videos/123 بعد 3s")).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(screen.getByRole("heading", { level: 1 }).textContent).not.toBe("Logs");
  });
});
