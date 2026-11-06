// Epic 53 story 53.11 T01–T03: the Logs page over the log index: the search and its filters
// in the address, a reference opens Follow a request (every service, time order, gaps
// marked), Lines around this, the live tail pausing and counting while the admin reads
// further down, and the files view with its notice when the index is restarting.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../../lib/api";
import { renderPage } from "../../test/pageHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { AdminLogs } from "./Logs";

const RID = "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60";
const line = (msg: string, extra: Record<string, unknown> = {}) => ({
  ts: "2026-11-06T10:00:00Z",
  level: "error",
  service: "api",
  msg,
  ...extra,
});

class FakeSource {
  static last: FakeSource | null = null;
  onmessage: ((ev: { data: string; lastEventId: string }) => void) | null = null;
  closed = false;
  constructor(
    public url: string,
    public init?: EventSourceInit
  ) {
    FakeSource.last = this;
  }
  close() {
    this.closed = true;
  }
  send(id: number, l: unknown) {
    act(() => this.onmessage?.({ data: JSON.stringify(l), lastEventId: String(id) }));
  }
}

function open(path = "/admin/logs") {
  window.history.replaceState({}, "", path);
  return renderPage(<AdminLogs />, { path: "/admin/logs" });
}

const calls = (prefix: string) =>
  get.mock.calls.map(([u]) => String(u)).filter((u) => u.startsWith(prefix));

describe("Logs over the index", () => {
  beforeEach(() => {
    vi.stubGlobal("EventSource", FakeSource);
    get.mockImplementation((path: string) => {
      if (path.startsWith("/api/admin/logs/users"))
        return Promise.resolve({ users: { abcd1234abcd1234: "sara" } });
      if (path.startsWith("/api/admin/logs/around"))
        return Promise.resolve({ lines: [line("before"), line("the one")], ids: [4, 5] });
      if (path.includes("request_id="))
        return Promise.resolve({
          lines: [
            line("web_clicked", {
              service: "web",
              ts: "2026-11-06T10:00:00.000Z",
              request_id: RID,
            }),
            line("api_got_it", { ts: "2026-11-06T10:00:00.200Z", request_id: RID }),
            line("stream_late", {
              service: "streaming",
              ts: "2026-11-06T10:00:03Z",
              request_id: RID,
            }),
          ],
          ids: [1, 2, 3],
        });
      return Promise.resolve({
        lines: [line("plex_fetch_failed", { request_id: RID, user_h: "abcd1234abcd1234" })],
        ids: [5],
      });
    });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    get.mockReset();
    window.history.replaceState({}, "", "/");
  });

  it("T01: searches with the address's filters, keeps new ones there, and names the user", async () => {
    open("/admin/logs?services=api&level=warn&components=core.api");
    expect(await screen.findByText("plex_fetch_failed")).toBeInTheDocument();
    expect(calls("/api/admin/logs/search")[0]).toContain("service=api");
    expect(calls("/api/admin/logs/search")[0]).toContain("level=warn");
    expect(calls("/api/admin/logs/search")[0]).toContain("component=core.api");
    expect(screen.getByText("sara")).toBeInTheDocument();
    const box = screen.getByRole("textbox", { name: en["logs.search"] });
    await userEvent.setup().type(box, '"connection refused" -disk{Enter}');
    await waitFor(() =>
      expect(calls("/api/admin/logs/search").at(-1)).toContain("q=%22connection+refused%22+-disk")
    );
    expect(window.location.search).toContain("q=%22connection+refused%22+-disk");
    expect(window.location.search).toContain("services=api");
  });

  it("T01: a reference opens Follow a request: every service in time order, a gap marked", async () => {
    open();
    await screen.findByText("plex_fetch_failed");
    const box = screen.getByRole("textbox", { name: en["logs.search"] });
    await userEvent.setup().type(box, "01932F6E{Enter}");
    const f = await screen.findByTestId("follow-request");
    await waitFor(() => expect(within(f).getAllByTestId("index-row")).toHaveLength(3));
    expect(calls("/api/admin/logs/search").at(-1)).toContain("request_id=01932f6e");
    const rows = within(f).getAllByTestId("index-row");
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("web_clicked"),
      expect.stringContaining("api_got_it"),
      expect.stringContaining("stream_late"),
    ]);
    expect(rows[1].className).not.toContain("--gap");
    expect(rows[2].className).toContain("--gap"); // 2.8 s after the one before
  });

  it("T01: a line's request chip follows it; Lines around this asks /around", async () => {
    open();
    await screen.findByText("plex_fetch_failed");
    fireEvent.click(screen.getByRole("button", { name: en["logs.around"] }));
    const box = await screen.findByTestId("logs-around");
    expect(within(box).getByText("the one")).toBeInTheDocument();
    expect(calls("/api/admin/logs/around")[0]).toContain("id=5");
    fireEvent.click(screen.getAllByTitle(en["logs.follow"])[0]);
    expect(await screen.findByTestId("follow-request")).toHaveTextContent(RID);
  });

  it("T02: Live tails the index; reading further down pauses it and counts the new lines", async () => {
    open("/admin/logs?level=warn");
    await screen.findByText("plex_fetch_failed");
    fireEvent.click(screen.getByRole("checkbox", { name: en["logs.live"] }));
    await waitFor(() => expect(FakeSource.last).not.toBeNull());
    const src = FakeSource.last as FakeSource;
    expect(src.url).toContain("/api/admin/logs/tail?");
    expect(src.url).toContain("level=warn");
    expect(src.init?.withCredentials).toBe(true);
    src.send(10, line("first_live"));
    expect(await screen.findByText("first_live")).toBeInTheDocument();
    const scroll = screen.getByTestId("logs-live-scroll");
    Object.defineProperty(scroll, "scrollTop", { configurable: true, value: 300, writable: true });
    fireEvent.scroll(scroll);
    src.send(11, line("while_reading_1"));
    src.send(12, line("while_reading_2"));
    expect(screen.queryByText("while_reading_1")).toBeNull();
    const jump = await screen.findByTestId("logs-new-lines");
    expect(jump).toHaveTextContent(en["logs.newLines"].replace("{count}", "2"));
    fireEvent.click(jump);
    expect(screen.getByText("while_reading_1")).toBeInTheDocument();
    expect(screen.queryByTestId("logs-new-lines")).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: en["logs.live"] }));
    expect(src.closed).toBe(true);
  });

  it("T03: with the index restarting (503), the files view with its notice", async () => {
    get.mockImplementation((path: string) => {
      if (path.startsWith("/api/admin/logs/search"))
        return Promise.reject(
          new ApiError({ type: "about:blank", title: "log search is not running", status: 503 })
        );
      if (path.startsWith("/api/admin/logs/stream"))
        return Promise.resolve({ entries: [line("from_the_ring")], count: 1 });
      return Promise.resolve({ entries: [], files: [] });
    });
    open();
    expect(await screen.findByTestId("logs-files-only")).toHaveTextContent(en["logs.filesOnly"]);
    expect(await screen.findByText("from_the_ring")).toBeInTheDocument();
  });

  it("T04: every string of these pages is in English and Arabic", () => {
    const keys = Object.keys(en).filter(
      (k) => k.startsWith("logs.") || k.startsWith("errors.") || k.startsWith("nav.admin.errors")
    );
    const enMap = en as Record<string, string>;
    const arMap = ar as Record<string, string>;
    for (const k of keys) {
      expect(arMap[k], k).toBeTruthy();
      const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join();
      expect(vars(arMap[k]), k).toBe(vars(enMap[k]));
    }
  });
});
