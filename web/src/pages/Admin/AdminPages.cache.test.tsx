// The admin list pages keep their last answer (lib/usePageData): a return
// shows it at once and reads it again quietly; a change the page makes is
// kept too, so a return never shows what was deleted. Channels stands in
// for the admin lists; the first read waits 0.4 s before its placeholder.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage } from "../../test/pageHarness";

const { get, del } = vi.hoisted(() => ({ get: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, delete: del } };
});

import { Channels } from "./Channels";

const channel = (id: string, number: number, name: string) => ({
  id,
  number,
  name,
  category: "",
  mode: "shuffle",
  enabled: true,
  sort_order: number,
});
const LIST = { items: [channel("c1", 1, "Movies"), channel("c2", 2, "News")] };

/** GET /api/channels answers `body`, or never when `body` is undefined. */
function serve(body?: unknown) {
  get.mockImplementation((url: string) => {
    if (url === "/api/channels") {
      return body === undefined ? new Promise(() => {}) : Promise.resolve(body);
    }
    return Promise.resolve({});
  });
}

const busy = () => document.querySelector('[aria-busy="true"]');
const listReads = () => get.mock.calls.filter(([u]) => u === "/api/channels").length;

describe("admin list pages keep their last answer", () => {
  beforeEach(() => {
    get.mockReset();
    del.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("the first read shows its table-shaped placeholder only after 0.4 s", () => {
    vi.useFakeTimers();
    serve();
    renderPage(<Channels />, { path: "/admin/channels" });
    expect(busy()).not.toBeNull();
    expect(document.querySelector(".mk-skeleton")).toBeNull();
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(busy()!.querySelector("table.mkt-table.mkt-cb-list .mk-skeleton")).not.toBeNull();
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("a return shows the kept list at once and reads it again quietly", async () => {
    serve(LIST);
    renderPage(<Channels />, { path: "/admin/channels" });
    expect(await screen.findByText("Movies")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    serve(); // the next read never answers: what shows is the kept list
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("Movies")).toBeInTheDocument();
    expect(busy()).toBeNull();
    expect(listReads()).toBe(2);
  });

  it("a deleted channel is not in the list a return shows", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    del.mockResolvedValue(undefined);
    serve(LIST);
    renderPage(<Channels />, { path: "/admin/channels" });
    const news = (await screen.findByText("News")).closest("tr")!;

    serve(); // no read answers from here on: only the page's own change counts
    const user = userEvent.setup();
    await user.click(within(news).getByRole("button", { name: "Delete" }));
    expect(del).toHaveBeenCalledWith("/api/channels/c2");
    expect(screen.queryByText("News")).toBeNull();

    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("Movies")).toBeInTheDocument();
    expect(screen.queryByText("News")).toBeNull();
  });
});
